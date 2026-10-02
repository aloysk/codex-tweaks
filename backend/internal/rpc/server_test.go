package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codex-tweaks/codex-tweaks/backend/internal/core"
)

func TestServerPingAndPreInitializationGuard(t *testing.T) {
	input := strings.NewReader(
		"{\"id\":1,\"method\":\"ping\"}\n" +
			"{\"id\":2,\"method\":\"getState\"}\n",
	)
	var output bytes.Buffer
	if err := NewServer(input, &output).Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	lines := responseLines(t, output.String())
	if got := lines[0].Result["backend"]; got != "go" {
		t.Fatalf("backend = %#v", got)
	}
	if got := lines[0].Result["protocolVersion"]; got != float64(core.ProtocolVersion) {
		t.Fatalf("protocolVersion = %#v", got)
	}
	if lines[1].Error == nil || !strings.Contains(lines[1].Error.Message, "initialize") {
		t.Fatalf("expected initialization error, got %#v", lines[1])
	}
}

func TestAppearanceRPCRejectsInvalidDraftWithoutChangingSavedSettings(t *testing.T) {
	session := newAppearanceRPCSession(t, &isolatedCDP{}, false)
	settings := core.DefaultAppearanceSettings()
	settings.Theme, settings.BackgroundMode, settings.SolidColor = "mint", "solid", "invalid"
	session.send(t, 2, "appearance.preview", map[string]any{"settings": settings})
	if frame := session.await(t, 2); frame.Error == nil || frame.Error.Message != "appearance.error.invalidSettings" {
		t.Fatalf("invalid draft accepted: %#v", frame)
	}
	session.send(t, 3, "getState", nil)
	appearance := session.await(t, 3).Result["appearance"].(map[string]any)
	saved := appearance["saved"].(map[string]any)
	if saved["theme"] != "native" || saved["solidColor"] != "#DEF3E5" || appearance["preview"] != nil || appearance["errorTextKey"] != "appearance.error.invalidSettings" {
		t.Fatalf("invalid draft changed saved/native state: %#v", appearance)
	}
	session.send(t, 4, "shutdown", nil)
	if frame := session.await(t, 4); frame.Error != nil {
		t.Fatalf("shutdown: %#v", frame.Error)
	}
	session.finish(t)
}

func TestServerInitializesControllerWithoutBackgroundSideEffects(t *testing.T) {
	root := t.TempDir()
	params := `{
      "applicationSupportDirectory": ` + quoted(filepath.Join(root, "support")) + `,
      "cacheDirectory": ` + quoted(filepath.Join(root, "cache")) + `,
      "preferredLanguages": ["ja-JP"],
      "currentVersion": "0.1.0",
      "buildNumber": "1"
    }`
	initializeRequest, err := json.Marshal(request{
		ID: 1, Method: "initialize", Params: json.RawMessage(params),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(
		string(initializeRequest) + "\n" +
			"{\"id\":2,\"method\":\"setDisableGPUAcceleration\",\"params\":{\"enabled\":true}}\n" +
			"{\"id\":3,\"method\":\"setLanguage\",\"params\":{\"language\":\"ko\"}}\n" +
			"{\"id\":4,\"method\":\"getState\"}\n" +
			"{\"id\":5,\"method\":\"shutdown\"}\n",
	)
	var output bytes.Buffer
	server := NewServerWithDependencies(
		input,
		&output,
		core.ControllerDependencies{DisableBackground: true, Platform: isolatedPlatform{}, CDP: &isolatedCDP{}},
	)
	if err := server.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var initialize struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion int `json:"protocolVersion"`
			Presentation    struct {
				Locale             string `json:"locale"`
				LanguagePreference string `json:"languagePreference"`
			} `json:"presentation"`
		} `json:"result"`
	}
	foundInitialize := false
	var state struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion           int  `json:"protocolVersion"`
			DeveloperAllowUnknownNode bool `json:"developerAllowUnknownNode"`
			DisableGPUAcceleration    bool `json:"disableGPUAcceleration"`
			Presentation              struct {
				Locale             string `json:"locale"`
				LanguagePreference string `json:"languagePreference"`
			} `json:"presentation"`
		} `json:"result"`
	}
	foundState := false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var header struct {
			ID *int `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			t.Fatalf("decode header: %v", err)
		}
		if header.ID != nil && *header.ID == 1 {
			if err := json.Unmarshal([]byte(line), &initialize); err != nil {
				t.Fatalf("decode initialize: %v", err)
			}
			foundInitialize = true
		}
		if header.ID != nil && *header.ID == 4 {
			if err := json.Unmarshal([]byte(line), &state); err != nil {
				t.Fatalf("decode getState: %v", err)
			}
			foundState = true
		}
	}
	if !foundInitialize || initialize.Result.ProtocolVersion != core.ProtocolVersion ||
		initialize.Result.Presentation.Locale != "ja" || initialize.Result.Presentation.LanguagePreference != "auto" {
		t.Fatalf("initialize response not found in %q", output.String())
	}
	if !foundState || state.Result.ProtocolVersion != core.ProtocolVersion || state.Result.DeveloperAllowUnknownNode ||
		!state.Result.DisableGPUAcceleration ||
		state.Result.Presentation.Locale != "ko" || state.Result.Presentation.LanguagePreference != "ko" {
		t.Fatalf("getState did not expose the v10 settings and Node defaults: %q", output.String())
	}
}

type decodedResponse struct {
	ID     int                    `json:"id"`
	Result map[string]interface{} `json:"result"`
	Error  *rpcError              `json:"error"`
}

func responseLines(t *testing.T, output string) []decodedResponse {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]decodedResponse, 0, len(lines))
	for _, line := range lines {
		var response decodedResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		result = append(result, response)
	}
	return result
}

func quoted(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type failingWriter struct {
	err   error
	short bool
	calls int
}

func (w *failingWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.short {
		return len(data) - 1, nil
	}
	return 0, w.err
}

func TestServerPropagatesOutputFailure(t *testing.T) {
	for _, input := range []string{
		"{\"id\":1,\"method\":\"ping\"}\n",
		"not JSON\n",
		"{\"id\":0,\"method\":\"ping\"}\n",
		"{\"id\":1,\"method\":\"getState\"}\n",
	} {
		t.Run(input, func(t *testing.T) {
			output := &failingWriter{err: io.ErrClosedPipe}
			err := NewServer(strings.NewReader(input), output).Serve()
			if !errors.Is(err, io.ErrClosedPipe) || output.calls != 1 {
				t.Fatalf("error=%v, writes=%d", err, output.calls)
			}
		})
	}
}

func TestServerRejectsShortOutputAndEncodingFailure(t *testing.T) {
	output := &failingWriter{short: true}
	server := NewServer(strings.NewReader(""), output)
	if err := server.write(response{ID: 1, Result: true}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
	if err := server.write(response{ID: 2, Result: true}); !errors.Is(err, io.ErrShortWrite) || output.calls != 1 {
		t.Fatalf("failed output reused: error=%v, writes=%d", err, output.calls)
	}
	var buffer bytes.Buffer
	server = NewServer(strings.NewReader(""), &buffer)
	var unsupported *json.UnsupportedTypeError
	if err := server.write(make(chan int)); !errors.As(err, &unsupported) || buffer.Len() != 0 {
		t.Fatalf("encoding failure was lost: %v", err)
	}
}

func TestServerEventFailureUnblocksClosableInput(t *testing.T) {
	input, peer := io.Pipe()
	defer input.Close()
	defer peer.Close()
	server := NewServer(input, &failingWriter{err: io.ErrClosedPipe})
	finished := make(chan error, 1)
	go func() { finished <- server.Serve() }()
	if err := server.write(event{Event: "state", Data: true}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("event write: %v", err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("serve lost transport failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failed event left the server blocked on input")
	}
}

func TestRequestErrorsPreserveOutcomeCategories(t *testing.T) {
	for _, item := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "timeout"},
		{errors.ErrUnsupported, "unsupported"},
		{errors.New("rejected"), "request_failed"},
	} {
		if got := requestErrorCode(item.err); got != item.code {
			t.Fatalf("code=%s, want=%s", got, item.code)
		}
	}
}

// All initialized RPC fixtures are isolated from the installed official app.
type isolatedPlatform struct{}

func (isolatedPlatform) IsCodexRunning(context.Context) (bool, error) { return false, nil }
func (isolatedPlatform) ObserveCodex(context.Context) (core.CodexObservation, error) {
	return core.CodexObservation{}, nil
}
func (isolatedPlatform) ActivateCodex(context.Context) error { return errors.ErrUnsupported }
func (isolatedPlatform) LaunchCodex(context.Context, core.CodexLaunchOptions) error {
	return errors.ErrUnsupported
}
func (isolatedPlatform) RestartCodex(context.Context, core.CodexLaunchOptions) error {
	return errors.ErrUnsupported
}
func (isolatedPlatform) Architecture() string { return "amd64" }

type isolatedCDP struct{}

func (*isolatedCDP) BindTarget(context.Context, *core.CodexProcessIdentity) error { return nil }
func (*isolatedCDP) Inject(context.Context, core.Payload, int) (core.CDPInjectionResult, error) {
	return core.CDPInjectionResult{}, errors.ErrUnsupported
}
func (*isolatedCDP) CleanupAllTargets(context.Context) (core.CDPCleanupResult, error) {
	return core.CDPCleanupResult{}, nil
}
func (*isolatedCDP) ReloadAllTargets(context.Context) (core.CDPReloadResult, error) {
	return core.CDPReloadResult{}, errors.ErrUnsupported
}
func (*isolatedCDP) SetNodeInvoker(core.NodeInvoker)     {}
func (*isolatedCDP) EmitNodeEvent(core.NodeRuntimeEvent) {}
