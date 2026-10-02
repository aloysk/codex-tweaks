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
	"time"

	"github.com/gorilla/websocket"
)

type appearanceCDPFixture struct {
	mu          sync.Mutex
	server      *httptest.Server
	targetIDs   []string
	expressions []string
	respond     func(string, string) map[string]any
}

func newAppearanceCDPFixture(t *testing.T, ids ...string) *appearanceCDPFixture {
	t.Helper()
	fixture := &appearanceCDPFixture{targetIDs: ids}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/json/list" {
			fixture.mu.Lock()
			ids := append([]string{}, fixture.targetIDs...)
			fixture.mu.Unlock()
			targets := []CDPTarget{}
			for _, id := range ids {
				url := "ws" + strings.TrimPrefix(fixture.server.URL, "http") + "/devtools/page/" + id
				targets = append(targets, CDPTarget{ID: id, Type: "page", URL: "app://-/index.html", WebSocketDebuggerURL: &url})
			}
			_ = json.NewEncoder(writer).Encode(targets)
			return
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var command struct {
			ID     int `json:"id"`
			Params struct {
				Expression string `json:"expression"`
			} `json:"params"`
		}
		if connection.ReadJSON(&command) != nil {
			return
		}
		fixture.mu.Lock()
		fixture.expressions = append(fixture.expressions, command.Params.Expression)
		respond := fixture.respond
		fixture.mu.Unlock()
		value := map[string]any{"status": "missing"}
		if respond != nil {
			value = respond(request.URL.Path, command.Params.Expression)
		}
		if value == nil {
			return
		}
		if (value["status"] == "applied" || value["status"] == "native") && value["settingsKey"] == nil {
			expression := command.Params.Expression
			var encoded string
			if _, rest, ok := strings.Cut(expression, "let settings = "); ok {
				encoded, _, _ = strings.Cut(rest, ";")
			} else if _, rest, ok := strings.Cut(expression, "const expectedSettingsKey = JSON.stringify("); ok {
				encoded, _, _ = strings.Cut(rest, ");")
			}
			var settings AppearanceSettings
			if json.Unmarshal([]byte(encoded), &settings) == nil {
				value["settingsKey"] = JSONLiteral(settings)
			}
		}
		_ = connection.WriteJSON(map[string]any{"id": command.ID, "result": map[string]any{"result": map[string]any{"value": value}}})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func TestCDPAppearanceLostAckThenSecondWindowCancelUsesAttemptedTarget(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "one")
	settings := DefaultAppearanceSettings()
	settings.Theme = "dark"
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.respond = func(path, expression string) map[string]any {
		if !strings.Contains(expression, "let settings =") {
			return map[string]any{"status": "missing"}
		}
		if strings.Contains(expression, `"theme":"dark"`) {
			close(entered)
			<-ctx.Done()
			return nil
		}
		return map[string]any{"status": "native", "targetID": strings.TrimPrefix(path, "/devtools/page/"), "revision": 2}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	type outcome struct {
		result AppearanceRuntimeResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := service.SetAppearance(ctx, AppearanceRuntimeRequest{Settings: settings, Revision: 1})
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("renderer dispatch did not reach the test page")
	}
	cancel()
	select {
	case pending := <-done:
		if pending.err == nil || pending.result.Status != "pending" || pending.result.TargetID != "one" {
			t.Fatalf("lost acknowledgement=%#v err=%v", pending.result, pending.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unacknowledged dispatch did not stop after cancellation")
	}
	fixture.mu.Lock()
	fixture.targetIDs = []string{"one", "two"}
	fixture.mu.Unlock()
	result, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: DefaultAppearanceSettings(), Revision: 2})
	if err != nil || result.TargetID != "one" || result.Status != "native" {
		t.Fatalf("cancel=%#v err=%v", result, err)
	}
}

func TestCDPAppearanceSameRevisionWrongSettingsCannotConfirmSavedState(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "main")
	dark := DefaultAppearanceSettings()
	dark.Theme = "dark"
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	fixture.respond = func(_ string, expression string) map[string]any {
		if !strings.Contains(expression, "let settings =") {
			return map[string]any{"status": "applied", "targetID": "main", "revision": 2, "settingsKey": JSONLiteral(dark)}
		}
		return map[string]any{"status": "applied", "targetID": "main", "revision": 2, "settingsKey": JSONLiteral(mint)}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	if result, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: mint, Revision: 2}); err != nil || result.Status != "applied" {
		t.Fatalf("restore saved=%#v err=%v", result, err)
	}
	if len(fixture.expressions) != 2 || !strings.Contains(fixture.expressions[1], `"theme":"mint"`) {
		t.Fatal("same revision wrong settings were accepted without reapplying")
	}
}

func TestCDPAppearanceRequiresTargetRevisionAndActualStatusConfirmation(t *testing.T) {
	for _, mode := range []string{"valid", "wrongTarget", "wrongRevision", "missingStatus", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newAppearanceCDPFixture(t, "main")
			fixture.respond = func(_ string, expression string) map[string]any {
				if !strings.Contains(expression, "let settings =") {
					return map[string]any{"status": "missing"}
				}
				value := map[string]any{"status": "applied", "targetID": "main", "revision": 7}
				switch mode {
				case "wrongTarget":
					value["targetID"] = "other"
				case "wrongRevision":
					value["revision"] = 6
				case "missingStatus":
					value["status"] = "accepted"
				case "foreign":
					value["status"] = "foreign"
				}
				return value
			}
			service := newSyntheticCDP(t, fixture.server.URL)
			settings := DefaultAppearanceSettings()
			settings.Theme = "mint"
			result, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, Revision: 7})
			if mode == "valid" {
				if err != nil || result.TargetID != "main" || result.Revision != 7 || result.Status != "applied" {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else if err == nil {
				t.Fatal("spoofed result accepted")
			}
			if len(service.attemptedTargets) != 1 {
				t.Fatal("attempt was not recorded before evaluation")
			}
		})
	}
}

func TestCDPAppearanceFailsClosedForAmbiguityAndKeepsFixedTargetOwnership(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "one", "two")
	fixture.respond = func(path, expression string) map[string]any {
		if strings.Contains(expression, "appearance.cleanup()") {
			return map[string]any{"status": "native"}
		}
		if strings.Contains(expression, "let settings =") {
			return map[string]any{"status": "applied", "targetID": strings.TrimPrefix(path, "/devtools/page/"), "revision": 4}
		}
		return map[string]any{"status": "missing"}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	if _, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, Revision: 4}); !errors.Is(err, ErrAppearanceAmbiguousTarget) {
		t.Fatalf("ambiguous=%v", err)
	}
	if len(fixture.expressions) != 0 {
		t.Fatal("ambiguous target was modified")
	}
	if result, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, TargetID: "one", Revision: 4}); err != nil || result.TargetID != "one" {
		t.Fatalf("selected=%#v err=%v", result, err)
	}
	if result, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, TargetID: "two", Revision: 4}); err != nil || result.TargetID != "two" {
		t.Fatalf("switched=%#v err=%v", result, err)
	}
	if len(fixture.expressions) != 5 || !strings.Contains(fixture.expressions[2], "appearance.cleanup()") {
		t.Fatalf("old target was not cleaned before the new one: %#v", fixture.expressions)
	}
}

func TestCDPAppearanceProcessBindingCleansOnlyOriginalOwnedTarget(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "main")
	fixture.respond = func(_ string, expression string) map[string]any {
		if strings.Contains(expression, "appearance.cleanup()") {
			return map[string]any{"status": "native"}
		}
		if strings.Contains(expression, "let settings =") {
			return map[string]any{"status": "applied", "targetID": "main", "revision": 1}
		}
		return map[string]any{"status": "missing"}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	settings := DefaultAppearanceSettings()
	settings.Theme = "dark"
	if _, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	identity := syntheticCodexIdentity()
	identity.StartedAt = identity.StartedAt.Add(time.Second)
	if err := service.BindTarget(context.Background(), &identity); err != nil {
		t.Fatal(err)
	}
	if service.appearanceTarget != nil || service.epoch != 2 || len(fixture.expressions) != 3 || !strings.Contains(fixture.expressions[2], "epoch = 1") {
		t.Fatal("appearance outlived original process identity")
	}
	if err := service.BindTarget(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, Revision: 2}); !errors.Is(err, ErrAppearanceUnavailable) {
		t.Fatalf("stopped=%v", err)
	}
}

func TestCDPAppearanceUnchangedProbeDoesNotResendImageBytes(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "main")
	fixture.respond = func(_ string, _ string) map[string]any {
		return map[string]any{"status": "applied", "targetID": "main", "revision": 9}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	if _, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, DataURL: "private-image-bytes-marker", Revision: 9}); err != nil {
		t.Fatal(err)
	}
	if len(fixture.expressions) != 1 || strings.Contains(fixture.expressions[0], "private-image-bytes-marker") {
		t.Fatal("unchanged probe retransmitted image")
	}
}

func TestCDPAppearanceFailedCleanupCannotAdoptAnotherProcessIdentity(t *testing.T) {
	fixture := newAppearanceCDPFixture(t, "main")
	fixture.respond = func(_ string, expression string) map[string]any {
		if strings.Contains(expression, "appearance.cleanup()") {
			return map[string]any{"status": "cleanupFailed"}
		}
		if strings.Contains(expression, "let settings =") {
			return map[string]any{"status": "applied", "targetID": "main", "revision": 1}
		}
		return map[string]any{"status": "missing"}
	}
	service := newSyntheticCDP(t, fixture.server.URL)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	if _, err := service.SetAppearance(context.Background(), AppearanceRuntimeRequest{Settings: settings, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	original := *service.boundTarget
	replacement := original
	replacement.StartedAt = replacement.StartedAt.Add(time.Second)
	if err := service.BindTarget(context.Background(), &replacement); !errors.Is(err, ErrAppearanceRenderer) {
		t.Fatalf("unconfirmed cleanup=%v", err)
	}
	if service.appearanceTarget == nil || !service.boundTarget.Equal(original) || service.epoch != 1 {
		t.Fatal("unconfirmed cleanup lost original ownership")
	}
}
