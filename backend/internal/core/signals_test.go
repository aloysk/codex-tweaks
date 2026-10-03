package core

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signalNumber(value float64) *float64 { return &value }

func signalTestController(t *testing.T) *Controller {
	t.Helper()
	root := t.TempDir()
	c, err := newTestController(InitializeParams{ApplicationSupportDirectory: filepath.Join(root, "support"), CacheDirectory: filepath.Join(root, "cache")}, nil, ControllerDependencies{DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	return c
}

func TestSignalsUnknownFieldsScopeAndStaleCache(t *testing.T) {
	now := time.Now()
	c := signalTestController(t)
	c.config.Enabled = true
	c.status.Kind = StatusConnected
	c.signals = signalsState{observedAt: now, observation: signalsObservation{
		Scope: "private-account", TargetID: "private-thread", CacheUpdatedAt: float64(now.UnixMilli()),
		Windows: []quotaObservationWindow{{UsedPercent: signalNumber(32), Seconds: signalNumber(18000)}, {Seconds: signalNumber(604800)}, {UsedPercent: signalNumber(math.NaN()), Seconds: signalNumber(60)}},
	}}
	snapshot := c.signalsSnapshotLocked(PresentationText(), now)
	if len(snapshot.Windows) != 1 || snapshot.Windows[0].Remaining != "68%" || snapshot.Windows[0].Title != "5 h" || snapshot.Task.Status != "unavailable" || snapshot.Rate.Status != "unavailable" {
		t.Fatalf("unexpected signals: %#v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-account") || strings.Contains(string(data), "private-thread") {
		t.Fatal("source identity leaked into presentation")
	}
	if snapshot.Quota.SourceUpdatedAt != PresentationText()["signals.timeUnknown"] {
		t.Fatal("cache update passed off as source update")
	}
	c.signals.observation.CacheUpdatedAt = float64(now.Add(-3 * time.Minute).UnixMilli())
	if c.signalsSnapshotLocked(PresentationText(), now).Quota.Status != "stale" {
		t.Fatal("re-read made old cache fresh")
	}
	c.status.Kind = StatusRestartRequired
	snapshot = c.signalsSnapshotLocked(PresentationText(), now)
	if len(snapshot.Windows) != 0 || snapshot.Quota.Status != "unavailable" {
		t.Fatal("disconnected numbers remained visible")
	}
}

type signalTestRuntime struct {
	CDPRuntime
	observation signalsObservation
	err         error
	reads       []bool
}

func (r *signalTestRuntime) readSignals(_ context.Context, full bool) (signalsObservation, error) {
	r.reads = append(r.reads, full)
	return r.observation, r.err
}

func TestSignalsSwitchAndReadBudget(t *testing.T) {
	c := signalTestController(t)
	c.config.Enabled = true
	runtime := &signalTestRuntime{CDPRuntime: c.cdp, observation: signalsObservation{Status: "ready", Scope: "one", TargetID: "page", Windows: []quotaObservationWindow{{UsedPercent: signalNumber(10), Seconds: signalNumber(60)}}}}
	c.cdp = runtime
	c.refreshSignals(context.Background(), c.runtimeEpoch)
	c.refreshSignals(context.Background(), c.runtimeEpoch)
	if len(runtime.reads) != 2 || !runtime.reads[0] || runtime.reads[1] {
		t.Fatalf("cache read budget: %v", runtime.reads)
	}
	runtime.observation.Scope = "two"
	c.refreshSignals(context.Background(), c.runtimeEpoch)
	if !c.signals.observedAt.IsZero() || len(c.signals.observation.Windows) != 0 {
		t.Fatal("account switch retained old numbers")
	}
	runtime.observation.Status = "unavailable"
	c.refreshSignals(context.Background(), c.runtimeEpoch)
	if !c.signals.observedAt.IsZero() {
		t.Fatal("ambiguous account retained numbers")
	}
}

func TestCapsulePersistsOnlyPreferencesAndRetainsStateOnSaveFailure(t *testing.T) {
	c := signalTestController(t)
	if c.Snapshot().Signals.Capsule.Enabled {
		t.Fatal("capsule enabled by default")
	}
	if err := c.SetCapsule(CapsuleSettings{Enabled: true, Collapsed: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(c.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"capsule"`) || strings.Contains(string(data), "signals") {
		t.Fatal("transient signals persisted")
	}
	c.configPath = c.store.StateDirectory // atomically replacing a directory must fail.
	if err := c.SetCapsule(CapsuleSettings{}); err == nil {
		t.Fatal("save failure not returned")
	}
	if !c.config.Capsule.Enabled || !c.config.Capsule.Collapsed {
		t.Fatal("failed save changed live preference")
	}
}

func TestSignalsRendererRejectsAmbiguityNullAndIdentityRace(t *testing.T) {
	fixture := `
const query = {queryKey:["rate-limit-status","account","user"], isActive:()=>true, state:{status:"success", dataUpdatedAt:100, data:{rate_limit:{primary_window:{used_percent:32,limit_window_seconds:18000,reset_at:200},secondary_window:{used_percent:null,limit_window_seconds:604800}}}}};
let queries = [query];
const client = {getQueryCache:()=>({getAll:()=>queries.slice()})};
const root = {memoizedProps:{client}};
const element = {__reactContainer$test:{current:root}};
globalThis.document = {documentElement:element, body:element, querySelectorAll:()=>[]};
const originalCrypto = globalThis.crypto;
Object.defineProperty(globalThis, 'crypto', {configurable:true, value:require('node:crypto').webcrypto});
`
	probe := "await eval(" + JSONLiteral(signalsProbeScript(true)) + ")"
	fixture += "let value = " + probe + `; assert.equal(value.status,'ready'); assert.equal(value.scope.length,64); assert.equal(value.windows[0].usedPercent,32); assert.equal(value.windows[1].usedPercent,null);
queries = [query,{...query,queryKey:["rate-limit-status","other","user"]}];
`
	fixture += "value = " + probe + `; assert.equal(value.status,'unavailable');
queries = [{...query, queryKey:["rate-limit-status"]}];
`
	fixture += "value = " + probe + `; assert.equal(value.status,'unavailable');
queries = [query];
Object.defineProperty(globalThis, 'crypto', {configurable:true, value:{subtle:{digest:async()=>{queries.push({...query});return new Uint8Array(32).buffer;}}}});
`
	fixture += "value = " + probe + `; assert.equal(value.status,'unavailable'); globalThis.crypto=originalCrypto;`
	runRendererDOMFixture(t, fixture)
}
