package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticCodexIdentity() CodexProcessIdentity {
	return CodexProcessIdentity{ProcessID: 42, ExecutablePath: "C:/synthetic/ChatGPT.exe", StartedAt: time.Unix(1000, 0), ApplicationID: "synthetic-official-codex"}
}

func newSyntheticCDP(t *testing.T, serverURL string) *CDPService {
	t.Helper()
	service := NewCDPService(nil, func(context.Context, CodexProcessIdentity) error { return nil })
	service.Endpoint = serverURL + "/json/list"
	service.AllowedOrigin = serverURL
	identity := syntheticCodexIdentity()
	if err := service.BindTarget(context.Background(), &identity); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCDPRefusesUnverifiedTargetWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	service := NewCDPService(nil)
	service.Endpoint = server.URL + "/json/list"
	service.AllowedOrigin = server.URL
	_, err := service.Inject(context.Background(), Payload{}, 0)
	if !errors.Is(err, ErrCodexIdentityUnverified) || calls.Load() != 0 {
		t.Fatalf("unverified access: err=%v calls=%d", err, calls.Load())
	}
}

func TestCDPRejectsHTTPRedirectAndForeignWebSocket(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	for _, mode := range []string{"redirect", "websocket"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
					return
				}
				debuggerURL := "ws" + strings.TrimPrefix(foreign.URL, "http") + "/devtools/page/codex-main"
				_ = json.NewEncoder(w).Encode([]CDPTarget{{ID: "codex-main", Type: "page", URL: "app://-/index.html", WebSocketDebuggerURL: &debuggerURL}})
			}))
			defer server.Close()
			_, err := newSyntheticCDP(t, server.URL).Inject(context.Background(), Payload{}, 0)
			if err == nil || foreignCalls.Load() != 0 {
				t.Fatalf("foreign authority reached: err=%v calls=%d", err, foreignCalls.Load())
			}
		})
	}
}

func TestCDPCancellationInterruptsBlockedWebSocket(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var command any
		if connection.ReadJSON(&command) != nil {
			return
		}
		close(started)
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()
	service := newSyntheticCDP(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan error, 1)
	go func() {
		_, err := service.execute(ctx, "Runtime.evaluate", map[string]any{}, "ws"+strings.TrimPrefix(server.URL, "http")+"/devtools/page/codex-main")
		completed <- err
	}()
	<-started
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation left websocket blocked")
	}
}

func newCDPSafetyFixture(t *testing.T, targetIDs []string, response func(string, string) (map[string]any, bool)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			targets := []CDPTarget{}
			for _, id := range targetIDs {
				ws := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/" + id
				targets = append(targets, CDPTarget{ID: id, Type: "page", URL: "app://-/index.html", WebSocketDebuggerURL: &ws})
			}
			_ = json.NewEncoder(w).Encode(targets)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var command map[string]any
		if connection.ReadJSON(&command) != nil {
			return
		}
		params, _ := command["params"].(map[string]any)
		expression, _ := params["expression"].(string)
		value, reply := response(strings.TrimPrefix(r.URL.Path, "/devtools/page/"), expression)
		if !reply {
			_, _, _ = connection.ReadMessage()
			return
		}
		_ = connection.WriteJSON(map[string]any{"id": command["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
	}))
	t.Cleanup(server.Close)
	return server
}

func fixtureInjectionResponse(expression string) map[string]any {
	if expression == targetDOMReadyProbeScript {
		return map[string]any{"ready": true}
	}
	if strings.Contains(expression, `status: "claimed"`) {
		return map[string]any{"status": "claimed"}
	}
	if strings.Contains(expression, "const host =") {
		return map[string]any{"status": "injected"}
	}
	return map[string]any{"status": "stale"}
}

func TestCDPCleanupAggregatesFailuresAndRetriesRemainingTargets(t *testing.T) {
	var mu sync.Mutex
	cleanupCalls := map[string]int{}
	server := newCDPSafetyFixture(t, []string{"first", "second"}, func(target, expression string) (map[string]any, bool) {
		if strings.Contains(expression, "const result = await runtime.cleanup()") {
			mu.Lock()
			defer mu.Unlock()
			cleanupCalls[target]++
			if target == "second" && cleanupCalls[target] == 1 {
				return map[string]any{"status": "cleanupFailed", "errors": []any{map[string]any{"message": "fixture callback failed"}}}, true
			}
			return map[string]any{"status": "cleaned"}, true
		}
		return fixtureInjectionResponse(expression), true
	})
	service := newSyntheticCDP(t, server.URL)
	if result, err := service.Inject(context.Background(), Payload{Version: "fixture"}, 0); err != nil || result.SuccessCount != 2 {
		t.Fatalf("inject=%#v err=%v", result, err)
	}
	result, err := service.CleanupAllTargets(context.Background())
	if err == nil || result.TargetCount != 2 || result.SuccessCount != 1 || result.Complete() {
		t.Fatalf("failed cleanup hidden: %#v err=%v", result, err)
	}
	result, err = service.CleanupAllTargets(context.Background())
	if err != nil || result.TargetCount != 1 || result.SuccessCount != 1 {
		t.Fatalf("cleanup retry=%#v err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if cleanupCalls["first"] != 1 || cleanupCalls["second"] != 2 {
		t.Fatalf("successful cleanup repeated: %v", cleanupCalls)
	}
}

func TestCDPExternalFailureBodiesStayInMemory(t *testing.T) {
	const canary = "a private conversation sentence without any credential format"
	server := newCDPSafetyFixture(t, []string{"main"}, func(_ string, expression string) (map[string]any, bool) {
		if strings.Contains(expression, "const result = await runtime.cleanup()") {
			return map[string]any{"status": "cleanupFailed", "errors": []any{map[string]any{"message": canary}}}, true
		}
		return fixtureInjectionResponse(expression), true
	})
	service := newSyntheticCDP(t, server.URL)
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.logger = logger
	if _, err := service.Inject(context.Background(), Payload{Version: "fixture"}, 0); err != nil {
		t.Fatal(err)
	}
	service.sessions["settings-fixture"] = &rendererBridgeSession{}
	service.logSettingsAdapterRuntimeError("settings-fixture", map[string]any{"settingsAdapterError": canary})
	delete(service.sessions, "settings-fixture")
	_, err = service.CleanupAllTargets(context.Background())
	if err == nil || !strings.Contains(err.Error(), canary) {
		t.Fatal("requested in-memory cleanup diagnostic was lost")
	}
	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview, canary) {
		t.Fatal("external body was persisted")
	}
}

func TestCDPTracksAnEvaluationBeforeItsAcknowledgement(t *testing.T) {
	var modified atomic.Bool
	server := newCDPSafetyFixture(t, []string{"main"}, func(_ string, expression string) (map[string]any, bool) {
		if strings.Contains(expression, "const result = await runtime.cleanup()") {
			return map[string]any{"status": "cleaned"}, true
		}
		if strings.Contains(expression, "const host =") {
			modified.Store(true)
			return nil, false
		}
		return fixtureInjectionResponse(expression), true
	})
	service := newSyntheticCDP(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := service.Inject(ctx, Payload{Version: "fixture"}, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("evaluation timeout=%v", err)
	}
	if !modified.Load() {
		t.Fatal("fixture did not make an unacknowledged change")
	}
	result, err := service.CleanupAllTargets(context.Background())
	if err != nil || result.TargetCount != 1 || !result.Complete() {
		t.Fatalf("unacknowledged target escaped cleanup: %#v err=%v", result, err)
	}
}

func TestCDPIdentityChangeStopsNetworkAndCannotCleanupReusedPID(t *testing.T) {
	identity := syntheticCodexIdentity()
	platform := &runtimeTestPlatform{observation: CodexObservation{Running: true, Target: &identity, ListenerOwned: true, ListenerObserved: true}}
	var calls atomic.Int32
	server := newCDPSafetyFixture(t, []string{"main"}, func(_ string, expression string) (map[string]any, bool) {
		calls.Add(1)
		return fixtureInjectionResponse(expression), true
	})
	service := NewCDPService(nil, func(ctx context.Context, expected CodexProcessIdentity) error {
		return verifyCodexTarget(ctx, platform, expected)
	})
	service.Endpoint = server.URL + "/json/list"
	service.AllowedOrigin = server.URL
	if err := service.BindTarget(context.Background(), &identity); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inject(context.Background(), Payload{Version: "fixture"}, 0); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	newIdentity := identity
	newIdentity.StartedAt = newIdentity.StartedAt.Add(time.Second)
	platform.setObservation(CodexObservation{Running: true, Target: &newIdentity, ListenerOwned: true, ListenerObserved: true})
	if _, err := service.Inject(context.Background(), Payload{}, 0); !errors.Is(err, ErrCodexTargetChanged) {
		t.Fatalf("PID reuse accepted: %v", err)
	}
	if result, err := service.CleanupAllTargets(context.Background()); !errors.Is(err, ErrCodexTargetChanged) || result.Complete() {
		t.Fatalf("wrong process cleaned: %#v err=%v", result, err)
	}
	if calls.Load() != before {
		t.Fatal("identity change reached reused renderer")
	}
	platform.setObservation(CodexObservation{ListenerObserved: true})
	if result, err := service.CleanupAllTargets(context.Background()); err != nil || !result.Complete() || result.Targets[0].Status != "exited" {
		t.Fatalf("verified process exit=%#v err=%v", result, err)
	}
}
