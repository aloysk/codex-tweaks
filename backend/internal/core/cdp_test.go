package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func TestInjectableTargetsSelectOnlyAppPages(t *testing.T) {
	data := []byte(`[
      {"id":"codex-main","type":"page","title":"Codex","url":"app://-/index.html","webSocketDebuggerUrl":"ws://127.0.0.1:9335/devtools/page/codex-main"},
      {"id":"browser","type":"page","title":"Example","url":"https://example.com","webSocketDebuggerUrl":"ws://127.0.0.1:9335/devtools/page/browser"},
      {"id":"worker","type":"service_worker","title":"Worker","url":"app://-/worker.js","webSocketDebuggerUrl":"ws://127.0.0.1:9335/devtools/page/worker"},
      {"id":"missing","type":"page","title":"Codex","url":"app://-/index.html"}
    ]`)
	targets, err := InjectableTargets(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ID != "codex-main" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestInjectUsesSmallProbeAfterInitialRuntimeSetup(t *testing.T) {
	const packageMarker = "full-package-bundle-marker"
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var mutex sync.Mutex
	expressions := []string{}
	runtimeCurrent := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/json/list":
			debuggerURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/codex-main"
			_ = json.NewEncoder(writer).Encode([]CDPTarget{{
				ID: "codex-main", Type: "page", URL: "app://-/index.html", WebSocketDebuggerURL: &debuggerURL,
			}})
		case "/devtools/page/codex-main":
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			command := map[string]any{}
			if connection.ReadJSON(&command) != nil {
				return
			}
			params, _ := command["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			mutex.Lock()
			expressions = append(expressions, expression)
			if expression == targetDOMReadyProbeScript {
				mutex.Unlock()
				_ = connection.WriteJSON(map[string]any{
					"id":     command["id"],
					"result": map[string]any{"result": map[string]any{"value": map[string]any{"ready": true}}},
				})
				return
			}
			isFullInjection := strings.Contains(expression, packageMarker)
			status := "stale"
			if strings.Contains(expression, `status: "claimed"`) {
				status = "claimed"
			} else if isFullInjection {
				runtimeCurrent = true
				status = "injected"
			} else if runtimeCurrent {
				status = "unchanged"
			}
			mutex.Unlock()
			_ = connection.WriteJSON(map[string]any{
				"id": command["id"],
				"result": map[string]any{"result": map[string]any{"value": map[string]any{
					"status": status, "packageErrors": []any{},
				}}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	service := newSyntheticCDP(t, server.URL)
	payload := Payload{Version: "stable", Packages: []CompiledPackage{{
		ID: "sample", Name: "sample", Version: "1.0.0",
		JavaScript: "module.exports.activate = () => {}; /* " + packageMarker + " */",
	}}}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.Inject(context.Background(), payload, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result.TargetCount != 1 || result.SuccessCount != 1 || result.FailureCount() != 0 {
			t.Fatalf("unexpected injection result on attempt %d: %#v", attempt+1, result)
		}
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(expressions) != 7 {
		t.Fatalf("CDP evaluate count = %d, want readiness + lease + probe + initial injection + readiness + lease + probe", len(expressions))
	}
	if expressions[0] != targetDOMReadyProbeScript || strings.Contains(expressions[1], packageMarker) || !strings.Contains(expressions[3], packageMarker) || expressions[4] != targetDOMReadyProbeScript || strings.Contains(expressions[6], packageMarker) {
		t.Fatalf("unexpected probe/full injection sequence: %#v", expressions)
	}
	if len(expressions[6]) >= len(expressions[3])/4 {
		t.Fatalf("unchanged runtime probe is not lightweight: probe=%d full=%d", len(expressions[6]), len(expressions[3]))
	}
}

func TestInjectWaitsForDOMReady(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ready := false
	fullInjectionCount := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/json/list":
			debuggerURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/codex-main"
			_ = json.NewEncoder(writer).Encode([]CDPTarget{{
				ID: "codex-main", Type: "page", URL: "app://-/index.html", WebSocketDebuggerURL: &debuggerURL,
			}})
		case "/devtools/page/codex-main":
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			command := map[string]any{}
			if connection.ReadJSON(&command) != nil {
				return
			}
			params, _ := command["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			value := map[string]any{"status": "stale", "packageErrors": []any{}}
			if expression == targetDOMReadyProbeScript {
				value = map[string]any{"ready": ready}
			} else if strings.Contains(expression, `status: "claimed"`) {
				value = map[string]any{"status": "claimed"}
			} else if strings.Contains(expression, "slow-start-package") {
				fullInjectionCount++
				value = map[string]any{"status": "injected", "packageErrors": []any{}}
			}
			_ = connection.WriteJSON(map[string]any{
				"id": command["id"], "result": map[string]any{"result": map[string]any{"value": value}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	service := newSyntheticCDP(t, server.URL)
	payload := Payload{Version: "stable", Packages: []CompiledPackage{{
		ID: "sample", Name: "sample", Version: "1.0.0",
		JavaScript: "module.exports.activate = () => {}; /* slow-start-package */",
	}}}

	result, err := service.Inject(context.Background(), payload, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCount != 0 || result.SuccessCount != 0 || fullInjectionCount != 0 {
		t.Fatalf("unready target was injected: result=%#v full=%d", result, fullInjectionCount)
	}

	ready = true
	result, err = service.Inject(context.Background(), payload, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCount != 1 || result.SuccessCount != 1 || fullInjectionCount != 1 {
		t.Fatalf("ready target was not injected: result=%#v full=%d", result, fullInjectionCount)
	}
}

func TestReloadAllTargetsPreservesActiveRenderer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	service := newSyntheticCDP(t, server.URL)
	result, err := service.ReloadAllTargets(context.Background())
	if !errors.Is(err, ErrRendererReloadUnsafe) || result.TargetCount != 0 || called {
		t.Fatalf("reload changed renderer: result=%#v err=%v called=%v", result, err, called)
	}
}
