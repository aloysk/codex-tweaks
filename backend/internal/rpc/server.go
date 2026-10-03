package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/codex-tweaks/codex-tweaks/backend/internal/core"
)

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     int64     `json:"id"`
	Result any       `json:"result,omitempty"`
	Error  *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type event struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

type Server struct {
	reader            io.Reader
	writer            io.Writer
	writeMu           sync.Mutex
	writeError        error
	abortOnce         sync.Once
	controller        *core.Controller
	dependencies      core.ControllerDependencies
	appearanceMu      sync.Mutex
	appearance        *appearanceOperation
	appearanceWorkers sync.WaitGroup
}

type appearanceOperation struct{ cancel context.CancelFunc }

func NewServer(reader io.Reader, writer io.Writer) *Server {
	return &Server{reader: reader, writer: writer}
}

func NewServerWithDependencies(
	reader io.Reader,
	writer io.Writer,
	dependencies core.ControllerDependencies,
) *Server {
	return &Server{
		reader: reader, writer: writer, dependencies: dependencies,
	}
}

func (s *Server) Serve() (serveError error) {
	defer func() {
		s.cancelAppearance()
		if s.controller != nil {
			serveError = errors.Join(serveError, s.controller.Shutdown())
		}
		s.appearanceWorkers.Wait()
		serveError = errors.Join(serveError, s.outputError())
	}()
	scanner := bufio.NewScanner(s.reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := s.outputError(); err != nil {
			return err
		}
		var incoming request
		if err := json.Unmarshal(scanner.Bytes(), &incoming); err != nil {
			if err := s.write(response{Error: &rpcError{Code: "invalid_request", Message: "Invalid JSON request"}}); err != nil {
				return err
			}
			continue
		}
		if incoming.ID == 0 || incoming.Method == "" {
			if err := s.write(response{ID: incoming.ID, Error: &rpcError{Code: "invalid_request", Message: "id 和 method 不能为空"}}); err != nil {
				return err
			}
			continue
		}
		if incoming.Method == "appearance.preview" || incoming.Method == "appearance.apply" || incoming.Method == "appearance.restoreNative" {
			if err := s.startAppearance(incoming); err != nil {
				return err
			}
			continue
		}
		if incoming.Method == "appearance.cancelPreview" || incoming.Method == "shutdown" {
			s.cancelAppearance()
		}
		result, err := s.dispatch(incoming)
		if incoming.Method == "shutdown" {
			// Drain the canceled appearance response before the client closes
			// its pipe after receiving the shutdown acknowledgement.
			s.appearanceWorkers.Wait()
		}
		if err := s.respond(incoming.ID, result, err); err != nil {
			return err
		}
		if incoming.Method == "shutdown" {
			return nil
		}
	}
	if err := s.outputError(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) respond(id int64, result any, err error) error {
	if err != nil {
		return s.write(response{ID: id, Error: &rpcError{Code: requestErrorCode(err), Message: err.Error()}})
	}
	return s.write(response{ID: id, Result: result})
}

// Preview/apply/restore run off the scanner loop, so cancel and shutdown remain
// reachable while the renderer is waiting. One pending operation bounds work;
// its context is reserved synchronously so a subsequent cancel also wins if
// the worker has not started yet. Initialization and other commands stay ordered.
func (s *Server) startAppearance(incoming request) error {
	if s.controller == nil {
		return s.respond(incoming.ID, nil, errors.New("请先调用 initialize"))
	}
	s.appearanceMu.Lock()
	if s.appearance != nil {
		s.appearanceMu.Unlock()
		return s.respond(incoming.ID, nil, core.ErrAppearanceUnavailable)
	}
	ctx, cancel := context.WithCancel(context.Background())
	operation := &appearanceOperation{cancel: cancel}
	s.appearance = operation
	s.appearanceWorkers.Add(1)
	s.appearanceMu.Unlock()
	go func() {
		defer s.appearanceWorkers.Done()
		defer cancel()
		result, err := s.dispatchAppearance(ctx, incoming)
		s.appearanceMu.Lock()
		// Release the slot as part of delivering the response. A caller that
		// immediately sends its next command waits for this short write rather
		// than racing the old slot; blocked output cannot grow a worker queue.
		_ = s.respond(incoming.ID, result, err) // write retains failures and wakes the scanner.
		if s.appearance == operation {
			s.appearance = nil
		}
		s.appearanceMu.Unlock()
	}()
	return nil
}

func (s *Server) cancelAppearance() {
	s.appearanceMu.Lock()
	defer s.appearanceMu.Unlock()
	if s.appearance != nil {
		s.appearance.cancel()
	}
}

func (s *Server) dispatchAppearance(ctx context.Context, incoming request) (any, error) {
	if incoming.Method == "appearance.restoreNative" {
		return s.controller.RestoreNativeAppearanceContext(ctx)
	}
	var params struct {
		Settings core.AppearanceSettings `json:"settings"`
	}
	if err := decodeParams(incoming.Params, &params); err != nil {
		return nil, err
	}
	if incoming.Method == "appearance.preview" {
		return s.controller.PreviewAppearanceContext(ctx, params.Settings)
	}
	return s.controller.ApplyAppearanceContext(ctx, params.Settings)
}

func requestErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errors.ErrUnsupported):
		return "unsupported"
	default:
		return "request_failed"
	}
}

func (s *Server) dispatch(incoming request) (any, error) {
	if incoming.Method == "ping" {
		return map[string]any{"protocolVersion": core.ProtocolVersion, "backend": "go"}, nil
	}
	if incoming.Method == "initialize" {
		if s.controller != nil {
			return nil, errors.New("后端已经初始化")
		}
		var params core.InitializeParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		controller, err := core.NewController(params, func(snapshot core.AppSnapshot) {
			// write retains transport failure and closes a closable input to wake Serve.
			_ = s.write(event{Event: "state", Data: snapshot})
		}, s.dependencies)
		if err != nil {
			return nil, err
		}
		s.controller = controller
		return controller.Snapshot(), nil
	}
	if s.controller == nil {
		return nil, errors.New("请先调用 initialize")
	}
	c := s.controller
	switch incoming.Method {
	case "getState":
		return c.Snapshot(), nil
	case "setCapsule":
		var params core.CapsuleSettings
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetCapsule(params))
	case "appearance.preview", "appearance.apply", "appearance.restoreNative":
		return s.dispatchAppearance(context.Background(), incoming)
	case "appearance.cancelPreview":
		return c.CancelAppearancePreview()
	case "appearance.importImage":
		var params struct {
			Path string `json:"path"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return c.ImportAppearanceImage(params.Path)
	case "setEnabled":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetEnabled(params.Enabled))
	case "setDisableGPUAcceleration":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetDisableGPUAcceleration(params.Enabled))
	case "setDeveloperMode":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetDeveloperMode(params.Enabled))
	case "setDeveloperAllowUnknownNode":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetDeveloperAllowUnknownNode(params.Enabled))
	case "authorizeNodePackage":
		var params struct {
			PackageID       string `json:"packageID"`
			AuthorizationID string `json:"authorizationID"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.AuthorizeNodePackage(params.PackageID, params.AuthorizationID))
	case "setPackageEnabled":
		var params struct {
			PackageID string `json:"packageID"`
			Enabled   bool   `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetPackageEnabled(params.PackageID, params.Enabled))
	case "setPackagePriority":
		var params struct {
			PackageID string `json:"packageID"`
			Priority  *int   `json:"priority"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetPackagePriority(params.PackageID, params.Priority))
	case "enableDependencies":
		var params packageIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.EnableDependencies(params.PackageID))
	case "reloadPackages":
		return accepted(c.ReloadPackages())
	case "checkNodeEnvironment":
		c.CheckNodeEnvironment()
		return accepted(nil)
	case "checkGitEnvironment":
		c.CheckGitEnvironment()
		return accepted(nil)
	case "buildPackage":
		var params packageIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.BuildPackage(params.PackageID))
	case "exportPackage":
		var params struct {
			PackageID       string `json:"packageID"`
			DestinationPath string `json:"destinationPath"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.ExportPackage(params.PackageID, params.DestinationPath))
	case "deletePackage":
		var params packageIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.DeletePackage(params.PackageID))
	case "installRemotePackage":
		var params struct {
			RepositoryURL string                  `json:"repositoryURL"`
			SelectorType  core.RemoteSelectorType `json:"selectorType"`
			SelectorValue string                  `json:"selectorValue"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		c.InstallRemotePackage(params.RepositoryURL, params.SelectorType, params.SelectorValue)
		return accepted(nil)
	case "installLocalPackage":
		var params struct {
			SourcePath string `json:"sourcePath"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		c.InstallLocalPackage(params.SourcePath)
		return accepted(nil)
	case "reportLocalPackageSelectionError":
		var params struct {
			Message string `json:"message"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		c.ReportLocalPackageSelectionError(params.Message)
		return accepted(nil)
	case "clearRemoteOperationFeedback":
		c.ClearRemoteOperationFeedback()
		return accepted(nil)
	case "clearLocalOperationFeedback":
		c.ClearLocalOperationFeedback()
		return accepted(nil)
	case "installMissingDependencies":
		var params packageIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.InstallMissingDependencies(params.PackageID))
	case "checkManagedPackageUpdates":
		var params struct {
			Automatic bool `json:"automatic"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		c.CheckManagedPackageUpdates(params.Automatic)
		return accepted(nil)
	case "updateManagedPackage":
		var params packageIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.UpdateManagedPackage(params.PackageID))
	case "openCodex":
		return accepted(c.OpenCodex())
	case "restartCodex":
		return accepted(c.RestartCodex())
	case "restartCodexUI":
		return accepted(c.RestartCodexUI())
	case "reinject":
		c.Reinject()
		return accepted(nil)
	case "refreshLog":
		c.RefreshLog()
		return accepted(nil)
	case "clearLog":
		return accepted(c.ClearLog())
	case "readAuthoringPrompt":
		return c.ReadAuthoringPrompt()
	case "checkAppUpdate":
		var params struct {
			Prompt bool `json:"prompt"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.CheckAppUpdate(params.Prompt))
	case "setUpdateChannel":
		var params struct {
			Channel core.UpdateChannel `json:"channel"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetUpdateChannel(params.Channel))
	case "setUpdateAutoCheck":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetUpdateAutoCheck(params.Enabled))
	case "setLanguage":
		var params struct {
			Language core.AppLanguage `json:"language"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SetLanguage(params.Language))
	case "dismissUpdate":
		c.DismissUpdate()
		return accepted(nil)
	case "skipUpdate":
		var params struct {
			TagName string `json:"tagName"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, err
		}
		return accepted(c.SkipUpdate(params.TagName))
	case "unskipAndPromptUpdate":
		return accepted(c.UnskipAndPromptUpdate())
	case "shutdown":
		if err := c.Shutdown(); err != nil {
			return nil, err
		}
		return core.ShutdownResult{Shutdown: true}, nil
	default:
		return nil, fmt.Errorf("未知方法：%s", incoming.Method)
	}
}

type packageIDParams struct {
	PackageID string `json:"packageID"`
}

func decodeParams(data json.RawMessage, destination any) error {
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	return json.Unmarshal(data, destination)
}

func accepted(err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]bool{"accepted": true}, nil
}

func (s *Server) write(value any) error {
	s.writeMu.Lock()
	if s.writeError != nil {
		err := s.writeError
		s.writeMu.Unlock()
		return err
	}
	data, err := json.Marshal(value)
	if err == nil {
		frame := append(data, '\n')
		var written int
		written, err = s.writer.Write(frame)
		if err == nil && written != len(frame) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		s.writeError = fmt.Errorf("RPC output failed: %w", err)
	}
	err = s.writeError
	s.writeMu.Unlock()
	if err != nil {
		s.abortOnce.Do(func() {
			if input, ok := s.reader.(io.Closer); ok {
				_ = input.Close()
			}
		})
	}
	return err
}

func (s *Server) outputError() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeError
}
