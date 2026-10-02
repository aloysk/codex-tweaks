package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrCDPEndpointUnavailable = errors.New("Codex 未开启本地 CDP 端口")
	ErrNoCodexUITargets       = errors.New("没有发现可重启的 Codex 界面")
	ErrRendererReloadUnsafe   = errors.New("请保存工作并从官方 Codex 正常退出；增强工具不会重载正在使用的页面")
	ErrRendererOwnership      = errors.New("页面已有其他增强运行时，请先退出该增强并正常重开 Codex")
)

const targetDOMReadyProbeScript = `(() => ({
  ready: Boolean(document.documentElement && document.head && document.body)
}))()`

type CDPTarget struct {
	ID                   string  `json:"id"`
	Type                 string  `json:"type"`
	Title                string  `json:"title"`
	URL                  string  `json:"url"`
	WebSocketDebuggerURL *string `json:"webSocketDebuggerUrl,omitempty"`
}

func (t CDPTarget) Injectable() bool {
	page, err := url.Parse(t.URL)
	if err != nil || t.ID == "" || t.Type != "page" || page.Scheme != "app" || page.Host != "-" || page.Path != "/index.html" || page.User != nil || t.WebSocketDebuggerURL == nil {
		return false
	}
	parsed, err := url.Parse(*t.WebSocketDebuggerURL)
	return err == nil && parsed.Scheme == "ws" && parsed.Hostname() == "127.0.0.1" && parsed.Port() != "" && parsed.User == nil && parsed.Path == "/devtools/page/"+t.ID && parsed.RawQuery == "" && parsed.Fragment == ""
}

func InjectableTargets(data []byte) ([]CDPTarget, error) {
	var targets []CDPTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, err
	}
	result := []CDPTarget{}
	for _, target := range targets {
		if target.Injectable() {
			result = append(result, target)
		}
	}
	return result, nil
}

type CDPInjectionResult struct {
	TargetCount   int               `json:"targetCount"`
	SuccessCount  int               `json:"successCount"`
	PackageErrors map[string]string `json:"packageErrors"`
	TargetErrors  map[string]string `json:"targetErrors"`
}

func (r CDPInjectionResult) FailureCount() int { return r.TargetCount - r.SuccessCount }

type CDPReloadResult struct {
	TargetCount  int `json:"targetCount"`
	SuccessCount int `json:"successCount"`
}

func (r CDPReloadResult) FailureCount() int { return r.TargetCount - r.SuccessCount }

type CDPCleanupTargetResult struct {
	TargetID string `json:"targetID"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type CDPCleanupResult struct {
	TargetCount  int                      `json:"targetCount"`
	SuccessCount int                      `json:"successCount"`
	Targets      []CDPCleanupTargetResult `json:"targets"`
}

func (r CDPCleanupResult) Complete() bool { return r.TargetCount == r.SuccessCount }

type TargetIdentityVerifier func(context.Context, CodexProcessIdentity) error
type attemptedCDPTarget struct {
	target   CDPTarget
	identity CodexProcessIdentity
}

type CDPService struct {
	Endpoint         string
	AllowedOrigin    string
	httpClient       *http.Client
	dialer           *websocket.Dialer
	logger           *Logger
	mu               sync.Mutex
	nextCommandID    int
	nodeInvoker      NodeInvoker
	sessions         map[string]*rendererBridgeSession
	verifyIdentity   TargetIdentityVerifier
	boundTarget      *CodexProcessIdentity
	attemptedTargets map[string]attemptedCDPTarget
	owner            string
	epoch            uint64
	stopped          bool
}

func NewCDPService(logger *Logger, verifier ...TargetIdentityVerifier) *CDPService {
	owner, err := randomHex(24)
	if err != nil {
		owner = ""
	} else {
		owner = ApplicationBundleIdentifier + ":" + owner
	}
	var verify TargetIdentityVerifier
	if len(verifier) > 0 {
		verify = verifier[0]
	}
	return &CDPService{
		Endpoint: CodexCDPTargetsURL, AllowedOrigin: CodexCDPOrigin,
		httpClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CDP redirects are forbidden") }},
		dialer:     &websocket.Dialer{HandshakeTimeout: 5 * time.Second, Proxy: nil},
		logger:     logger, nextCommandID: 1,
		sessions:       map[string]*rendererBridgeSession{},
		verifyIdentity: verify, attemptedTargets: map[string]attemptedCDPTarget{}, owner: owner,
	}
}

func lockWithContext(ctx context.Context, mutex *sync.Mutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mutex.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (s *CDPService) BindTarget(ctx context.Context, identity *CodexProcessIdentity) error {
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if identity == nil {
		s.boundTarget = nil
		s.stopped = true
		s.closeAllRendererSessionsLocked()
		return nil
	}
	if !identity.Valid() || s.verifyIdentity == nil || s.owner == "" {
		return ErrCodexIdentityUnverified
	}
	if err := s.verifyIdentity(ctx, *identity); err != nil {
		return err
	}
	if s.stopped || s.boundTarget == nil || !s.boundTarget.Equal(*identity) {
		s.closeAllRendererSessionsLocked()
		s.epoch++
		s.stopped = false
	}
	copy := *identity
	s.boundTarget = &copy
	return nil
}

func (s *CDPService) verifyBoundTarget(ctx context.Context) error {
	if s.boundTarget == nil || s.verifyIdentity == nil || !s.boundTarget.Valid() {
		return ErrCodexIdentityUnverified
	}
	return s.verifyIdentity(ctx, *s.boundTarget)
}

func (s *CDPService) debuggerAuthority(debuggerURL string) error {
	endpoint, err := url.Parse(s.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil || endpoint.Path != "/json/list" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return ErrCodexIdentityUnverified
	}
	if s.AllowedOrigin != endpoint.Scheme+"://"+endpoint.Host {
		return ErrCodexIdentityUnverified
	}
	if debuggerURL == "" {
		return nil
	}
	debugger, err := url.Parse(debuggerURL)
	if err != nil || debugger.Scheme != "ws" || debugger.Host != endpoint.Host || debugger.User != nil || debugger.RawQuery != "" || debugger.Fragment != "" || !strings.HasPrefix(debugger.Path, "/devtools/page/") {
		return ErrCodexIdentityUnverified
	}
	return nil
}

func (s *CDPService) Inject(ctx context.Context, payload Payload, forceGeneration int) (CDPInjectionResult, error) {
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return CDPInjectionResult{}, err
	}
	defer s.mu.Unlock()
	targets, err := s.discoverTargets(ctx)
	if err != nil {
		s.closeAllRendererSessionsLocked()
		return CDPInjectionResult{}, err
	}
	s.reconcileRendererSessionsLocked(targets)
	result := CDPInjectionResult{PackageErrors: map[string]string{}, TargetErrors: map[string]string{}}
	var failures []error
	fail := func(targetID string, err error) {
		result.TargetErrors[targetID] = err.Error()
		failures = append(failures, fmt.Errorf("target %s: %w", targetID, err))
	}
	for _, target := range targets {
		ready, err := s.targetDOMReady(ctx, *target.WebSocketDebuggerURL)
		if err != nil {
			s.logError(fmt.Sprintf("页面就绪检查失败：target=%s class=%s", target.ID, diagnosticErrorClass(err)))
			result.TargetCount++
			fail(target.ID, err)
			continue
		}
		if !ready {
			continue
		}
		result.TargetCount++
		s.trackAttempt(target)
		lease, err := s.evaluate(ctx, runtimeLeaseClaimScript(s.owner, s.epoch), *target.WebSocketDebuggerURL)
		if err != nil {
			fail(target.ID, err)
			continue
		}
		if lease["status"] != "claimed" {
			fail(target.ID, ErrRendererOwnership)
			continue
		}
		bridgeSessionID, nodeTokens, settingsAdapter, err := s.rendererBridgeForTargetLocked(ctx, target, payload)
		if err != nil {
			s.logError(fmt.Sprintf("能力通道建立失败：target=%s class=%s", target.ID, diagnosticErrorClass(err)))
			fail(target.ID, err)
			continue
		}
		probe := injectionRuntimeProbeScriptOwned(
			payload, forceGeneration, bridgeSessionID, settingsAdapter, s.owner, s.epoch,
		)
		value, probeErr := s.evaluate(ctx, probe, *target.WebSocketDebuggerURL)
		if probeErr != nil {
			fail(target.ID, probeErr)
			continue
		}
		if value["status"] == "foreign" {
			fail(target.ID, ErrRendererOwnership)
			continue
		}
		if probeErr == nil && value["status"] == "unchanged" {
			s.trackAttempt(target)
			result.SuccessCount++
			mergeInjectionPackageErrors(result.PackageErrors, value)
			s.logSettingsAdapterRuntimeError(target.ID, value)
			continue
		}
		if value["status"] != "stale" {
			fail(target.ID, errors.New("invalid renderer runtime probe"))
			continue
		}
		// An evaluation timeout does not prove that JavaScript made no changes.
		// Track the attempt before dispatch so a later stop includes this target.
		s.trackAttempt(target)
		script := injectionScriptOwned(payload, forceGeneration, bridgeSessionID, nodeTokens, settingsAdapter, s.owner, s.epoch)
		value, err = s.evaluate(ctx, script, *target.WebSocketDebuggerURL)
		if err != nil {
			s.logError(fmt.Sprintf("页面注入失败：target=%s class=%s", target.ID, diagnosticErrorClass(err)))
			fail(target.ID, err)
			continue
		}
		if value["status"] != "injected" && value["status"] != "unchanged" {
			fail(target.ID, errors.New("renderer did not confirm injection"))
			continue
		}
		result.SuccessCount++
		mergeInjectionPackageErrors(result.PackageErrors, value)
		s.logSettingsAdapterRuntimeError(target.ID, value)
	}
	return result, errors.Join(failures...)
}

func (s *CDPService) trackAttempt(target CDPTarget) {
	identity := *s.boundTarget
	key := SecureFingerprintStrings(JSONLiteral(identity), target.ID, *target.WebSocketDebuggerURL)
	s.attemptedTargets[key] = attemptedCDPTarget{target: target, identity: identity}
}

func (s *CDPService) logSettingsAdapterRuntimeError(targetID string, value map[string]any) {
	session := s.sessions[targetID]
	if session == nil {
		return
	}
	message, _ := value["settingsAdapterError"].(string)
	if message == session.settingsAdapterRuntimeError {
		return
	}
	session.settingsAdapterRuntimeError = message
	if message != "" {
		s.logError("Codex 设置适配失败：target=" + targetID + " class=renderer")
	}
}

func (s *CDPService) targetDOMReady(ctx context.Context, debuggerURL string) (bool, error) {
	value, err := s.evaluate(ctx, targetDOMReadyProbeScript, debuggerURL)
	if err != nil {
		return false, err
	}
	ready, _ := value["ready"].(bool)
	return ready, nil
}

func mergeInjectionPackageErrors(destination map[string]string, value map[string]any) {
	errorsValue, _ := value["packageErrors"].([]any)
	for _, rawError := range errorsValue {
		packageError, _ := rawError.(map[string]any)
		packageID, _ := packageError["id"].(string)
		message, _ := packageError["message"].(string)
		if packageID != "" && message != "" {
			destination[packageID] = message
		}
	}
}

func (s *CDPService) CleanupAllTargets(ctx context.Context) (CDPCleanupResult, error) {
	result := CDPCleanupResult{Targets: []CDPCleanupTargetResult{}}
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return result, err
	}
	defer s.mu.Unlock()
	defer s.closeAllRendererSessionsLocked()
	s.stopped = true
	originalBinding := s.boundTarget
	defer func() { s.boundTarget = originalBinding }()
	result.TargetCount = len(s.attemptedTargets)
	var failures []error
	for attemptKey, attempted := range s.attemptedTargets {
		targetID := attempted.target.ID
		item := CDPCleanupTargetResult{TargetID: targetID, Status: "pending"}
		var err error
		if s.verifyIdentity == nil {
			err = ErrCodexIdentityUnverified
		} else if err = s.verifyIdentity(ctx, attempted.identity); errors.Is(err, ErrCodexTargetExited) {
			err = nil
			item.Status = "exited"
		} else if err == nil {
			identity := attempted.identity
			s.boundTarget = &identity
			var value map[string]any
			value, err = s.evaluate(ctx, cleanupScriptOwned(s.owner, s.epoch), *attempted.target.WebSocketDebuggerURL)
			if err == nil && value["status"] != "cleaned" {
				err = fmt.Errorf("renderer cleanup not confirmed: %s", JSONLiteral(value))
			}
		}
		if err == nil {
			if item.Status == "pending" {
				item.Status = "cleaned"
			}
			result.SuccessCount++
			delete(s.attemptedTargets, attemptKey)
		} else {
			item.Error = err.Error()
			failures = append(failures, fmt.Errorf("target %s cleanup: %w", targetID, err))
			s.logError(fmt.Sprintf("页面清理失败：target=%s failed=1 class=%s", targetID, diagnosticErrorClass(err)))
		}
		result.Targets = append(result.Targets, item)
	}
	return result, errors.Join(failures...)
}

func (s *CDPService) ReloadAllTargets(ctx context.Context) (CDPReloadResult, error) {
	return CDPReloadResult{}, ErrRendererReloadUnsafe
}

func (s *CDPService) discoverTargets(ctx context.Context) ([]CDPTarget, error) {
	if err := s.verifyBoundTarget(ctx); err != nil {
		return nil, err
	}
	if err := s.debuggerAuthority(""); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Endpoint, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrCDPEndpointUnavailable
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("CDP 返回了无效响应")
	}
	var targets []CDPTarget
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&targets); err != nil {
		return nil, errors.New("CDP 返回了无效响应")
	}
	result := []CDPTarget{}
	seen := map[string]bool{}
	for _, target := range targets {
		if target.Injectable() {
			if err := s.debuggerAuthority(*target.WebSocketDebuggerURL); err != nil {
				return nil, err
			}
			if seen[target.ID] {
				return nil, errors.New("duplicate CDP target identity")
			}
			seen[target.ID] = true
			result = append(result, target)
		}
	}
	return result, nil
}

func (s *CDPService) evaluate(ctx context.Context, expression, debuggerURL string) (map[string]any, error) {
	response, err := s.execute(
		ctx,
		"Runtime.evaluate",
		map[string]any{
			"expression": expression, "returnByValue": true, "awaitPromise": true, "userGesture": false,
		},
		debuggerURL,
	)
	if err != nil {
		return nil, err
	}
	if exception, ok := response["exceptionDetails"].(map[string]any); ok {
		description := ""
		if exceptionValue, ok := exception["exception"].(map[string]any); ok {
			description, _ = exceptionValue["description"].(string)
		}
		if description == "" {
			description, _ = exception["text"].(string)
		}
		if description == "" {
			description = "注入脚本执行失败"
		}
		return nil, errors.New("CDP 拒绝执行：" + description)
	}
	remoteObject, _ := response["result"].(map[string]any)
	value, _ := remoteObject["value"].(map[string]any)
	return value, nil
}

func (s *CDPService) execute(
	ctx context.Context,
	method string,
	params map[string]any,
	debuggerURL string,
) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.verifyBoundTarget(ctx); err != nil {
		return nil, err
	}
	if err := s.debuggerAuthority(debuggerURL); err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Origin", s.AllowedOrigin)
	connection, _, err := s.dialer.DialContext(ctx, debuggerURL, header)
	if err != nil {
		return nil, cdpContextError(ctx, err)
	}
	defer connection.Close()
	connection.SetReadLimit(rendererBridgeMaximumPayload)
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	deadline := cdpCommandDeadline(ctx)
	_ = connection.SetWriteDeadline(deadline)
	_ = connection.SetReadDeadline(deadline)
	commandID := s.nextCommandID
	s.nextCommandID++
	command := map[string]any{
		"id": commandID, "method": method, "params": params,
	}
	if err := connection.WriteJSON(command); err != nil {
		return nil, cdpContextError(ctx, err)
	}
	for {
		_, message, err := connection.ReadMessage()
		if err != nil {
			return nil, cdpContextError(ctx, err)
		}
		var response map[string]any
		if err := json.Unmarshal(message, &response); err != nil {
			return nil, errors.New("CDP 返回了无法解析的消息")
		}
		responseID, ok := response["id"].(float64)
		if !ok || int(responseID) != commandID {
			continue
		}
		if commandError, ok := response["error"].(map[string]any); ok {
			message, _ := commandError["message"].(string)
			if message == "" {
				message = "未知错误"
			}
			return nil, errors.New("CDP 拒绝执行：" + message)
		}
		result, _ := response["result"].(map[string]any)
		return result, nil
	}
}

func cdpCommandDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}

func cdpContextError(ctx context.Context, err error) error {
	if contextError := ctx.Err(); contextError != nil {
		return contextError
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
	}
	return err
}

func (s *CDPService) logError(message string) {
	if s.logger != nil {
		s.logger.Error(message)
	}
}
