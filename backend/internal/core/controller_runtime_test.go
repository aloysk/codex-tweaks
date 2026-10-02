package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// All ordinary controller tests use this boundary, never a live CDP service or
// operating-system process adapter, even when a test enables a feature.
func newTestController(params InitializeParams, event func(AppSnapshot), dependencies ControllerDependencies) (*Controller, error) {
	if dependencies.CDP == nil {
		dependencies.CDP = &controllerTestCDP{}
	}
	if dependencies.Platform == nil {
		dependencies.Platform = idleControllerTestPlatform{}
	}
	return NewController(params, event, dependencies)
}

type controllerTestCDP struct{}

func (*controllerTestCDP) BindTarget(context.Context, *CodexProcessIdentity) error { return nil }
func (*controllerTestCDP) Inject(context.Context, Payload, int) (CDPInjectionResult, error) {
	return CDPInjectionResult{}, ErrCDPEndpointUnavailable
}
func (*controllerTestCDP) CleanupAllTargets(context.Context) (CDPCleanupResult, error) {
	return CDPCleanupResult{Targets: []CDPCleanupTargetResult{}}, nil
}
func (*controllerTestCDP) ReloadAllTargets(context.Context) (CDPReloadResult, error) {
	return CDPReloadResult{}, ErrRendererReloadUnsafe
}
func (*controllerTestCDP) SetNodeInvoker(NodeInvoker)     {}
func (*controllerTestCDP) EmitNodeEvent(NodeRuntimeEvent) {}

type runtimeTestPlatform struct {
	idleControllerTestPlatform
	mu          sync.Mutex
	observation CodexObservation
	launches    []CodexLaunchMode
	activations int
}

func (p *runtimeTestPlatform) ObserveCodex(context.Context) (CodexObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := p.observation
	if result.Target != nil {
		copy := *result.Target
		result.Target = &copy
	}
	return result, nil
}
func (p *runtimeTestPlatform) LaunchCodex(_ context.Context, options CodexLaunchOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.launches = append(p.launches, options.Mode)
	return nil
}
func (p *runtimeTestPlatform) ActivateCodex(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activations++
	return nil
}
func (p *runtimeTestPlatform) setObservation(observation CodexObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observation = observation
}

type runtimeTestCDP struct {
	controllerTestCDP
	inject  func(context.Context) (CDPInjectionResult, error)
	cleanup func(context.Context) (CDPCleanupResult, error)
}

func (r *runtimeTestCDP) Inject(ctx context.Context, _ Payload, _ int) (CDPInjectionResult, error) {
	if r.inject != nil {
		return r.inject(ctx)
	}
	return r.controllerTestCDP.Inject(ctx, Payload{}, 0)
}
func (r *runtimeTestCDP) CleanupAllTargets(ctx context.Context) (CDPCleanupResult, error) {
	if r.cleanup != nil {
		return r.cleanup(ctx)
	}
	return r.controllerTestCDP.CleanupAllTargets(ctx)
}

func runtimeTestController(t *testing.T, platform Platform, cdp CDPRuntime, event func(AppSnapshot)) *Controller {
	t.Helper()
	root := t.TempDir()
	c, err := newTestController(InitializeParams{ApplicationSupportDirectory: filepath.Join(root, "support"), CacheDirectory: filepath.Join(root, "cache")}, event, ControllerDependencies{Platform: platform, CDP: cdp, DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	return c
}
func waitRuntimeState(t *testing.T, c *Controller, kind AppStatusKind) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Snapshot().Status.Kind == kind {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("wanted %s, got %#v", kind, c.Snapshot().Status)
}

func TestControllerMonitorIsPassiveAndLaunchModesAreExplicit(t *testing.T) {
	platform := &runtimeTestPlatform{}
	c := runtimeTestController(t, platform, &controllerTestCDP{}, nil)
	if c.Snapshot().Enabled {
		t.Fatal("new installations must start disabled")
	}
	c.Refresh()
	if err := c.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	waitRuntimeState(t, c, StatusCodexNotRunning)
	platform.mu.Lock()
	count := len(platform.launches)
	platform.mu.Unlock()
	if count != 0 {
		t.Fatal("monitor launched the official app")
	}
	if err := c.OpenCodex(); err != nil {
		t.Fatal(err)
	}
	if err := c.RestartCodex(); err != nil {
		t.Fatal(err)
	}
	platform.mu.Lock()
	modes := append([]CodexLaunchMode{}, platform.launches...)
	platform.mu.Unlock()
	if len(modes) != 2 || modes[0] != CodexLaunchNormal || modes[1] != CodexLaunchEnhanced {
		t.Fatalf("launch modes=%v", modes)
	}
	platform.setObservation(CodexObservation{Running: true})
	if err := c.RestartCodex(); !errors.Is(err, ErrManualCodexExitRequired) {
		t.Fatalf("running enhanced launch=%v", err)
	}
	if err := c.OpenCodex(); err != nil {
		t.Fatal(err)
	}
	if err := c.RestartCodexUI(); !errors.Is(err, ErrRendererReloadUnsafe) {
		t.Fatalf("renderer reload=%v", err)
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.launches) != 2 || platform.activations != 1 {
		t.Fatalf("existing work affected: modes=%v activations=%d", platform.launches, platform.activations)
	}
}

func TestControllerDisableCancelsInjectionAndRejectsStaleConnectedResult(t *testing.T) {
	identity := syntheticCodexIdentity()
	platform := &runtimeTestPlatform{observation: CodexObservation{Running: true, Target: &identity, ListenerPresent: true, ListenerOwned: true, ListenerObserved: true}}
	started := make(chan struct{})
	runtime := &runtimeTestCDP{inject: func(ctx context.Context) (CDPInjectionResult, error) {
		close(started)
		<-ctx.Done()
		return CDPInjectionResult{TargetCount: 1, SuccessCount: 1}, nil
	}}
	var eventMu sync.Mutex
	staleConnected := false
	c := runtimeTestController(t, platform, runtime, func(snapshot AppSnapshot) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if !snapshot.Enabled && snapshot.Status.Kind == StatusConnected {
			staleConnected = true
		}
	})
	if err := c.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("injection did not start")
	}
	if err := c.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	got := c.Snapshot()
	eventMu.Lock()
	stale := staleConnected
	eventMu.Unlock()
	if stale || got.Enabled || got.Status.Kind != StatusDisabled || !got.Runtime.Recovery.InjectionStopped || got.Runtime.Recovery.DebugListener != "open" {
		t.Fatalf("unsafe disabled result: stale=%v snapshot=%#v", stale, got.Runtime)
	}
}

func TestControllerCleanupFailureStaysPendingAndCanBeRetried(t *testing.T) {
	identity := syntheticCodexIdentity()
	platform := &runtimeTestPlatform{observation: CodexObservation{Running: true, Target: &identity, ListenerPresent: true, ListenerOwned: true, ListenerObserved: true}}
	calls := 0
	runtime := &runtimeTestCDP{cleanup: func(context.Context) (CDPCleanupResult, error) {
		calls++
		if calls == 1 {
			return CDPCleanupResult{TargetCount: 1, Targets: []CDPCleanupTargetResult{{TargetID: "main", Status: "pending", Error: "fixture callback failed"}}}, errors.New("fixture callback failed")
		}
		return CDPCleanupResult{TargetCount: 1, SuccessCount: 1, Targets: []CDPCleanupTargetResult{{TargetID: "main", Status: "cleaned"}}}, nil
	}}
	c := runtimeTestController(t, platform, runtime, nil)
	if err := c.SetEnabled(false); err == nil {
		t.Fatal("cleanup failure was hidden")
	}
	if got := c.Snapshot(); got.Status.Kind != StatusRecoveryPending || got.Runtime.Recovery.PageCleanup != "pending" {
		t.Fatalf("failed cleanup reported complete: %#v", got.Runtime)
	}
	if err := c.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot(); got.Runtime.Recovery.PageCleanup != "confirmed" || got.Runtime.Recovery.DebugListener != "open" {
		t.Fatalf("cleanup closed listener without evidence: %#v", got.Runtime)
	}
	platform.setObservation(CodexObservation{ListenerObserved: true})
	c.Refresh()
	if got := c.Snapshot().Runtime.Recovery.DebugListener; got != "closed" {
		t.Fatalf("manual normal reopen observation=%s", got)
	}
}

func TestControllerDisableDoesNotPublishConnectedWhileCleaning(t *testing.T) {
	platform := &runtimeTestPlatform{}
	var eventMu sync.Mutex
	stale := false
	c := runtimeTestController(t, platform, &controllerTestCDP{}, func(snapshot AppSnapshot) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if !snapshot.Enabled && snapshot.Status.Kind == StatusConnected {
			stale = true
		}
	})
	c.mu.Lock()
	c.config.Enabled = true
	c.status = AppStatus{Kind: StatusConnected, TargetCount: 1}
	c.mu.Unlock()
	if err := c.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	eventMu.Lock()
	defer eventMu.Unlock()
	if stale {
		t.Fatal("disable emitted stale connected state")
	}
}

func TestControllerShutdownBoundsRuntimeLockWaitAndIsIdempotent(t *testing.T) {
	c := runtimeTestController(t, &runtimeTestPlatform{}, &controllerTestCDP{}, nil)
	c.runtimeMu.Lock()
	start := time.Now()
	err := c.Shutdown()
	c.runtimeMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > ShutdownTimeout+500*time.Millisecond {
		t.Fatalf("shutdown budget exceeded: elapsed=%s err=%v", time.Since(start), err)
	}
	if again := c.Shutdown(); !errors.Is(again, context.DeadlineExceeded) {
		t.Fatalf("repeated shutdown changed result: %v", again)
	}
}

func TestControllerRuntimePreferencesDoNotPublishFailedPersistence(t *testing.T) {
	c := runtimeTestController(t, &runtimeTestPlatform{}, &controllerTestCDP{}, nil)
	path := c.configPath
	c.configPath = filepath.Dir(c.configPath)
	if err := c.SetEnabled(true); err == nil {
		t.Fatal("enable save failure accepted")
	}
	if c.Snapshot().Enabled {
		t.Fatal("unsaved enable was published")
	}
	if err := c.SetDeveloperMode(true); err == nil {
		t.Fatal("developer save failure accepted")
	}
	if c.Snapshot().DeveloperMode {
		t.Fatal("unsaved developer mode was published")
	}
	c.configPath = path
	if err := c.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !c.Snapshot().Enabled {
		t.Fatal("enable retry did not take effect")
	}
}

func TestControllerQueuedRefreshCannotRewriteStateAfterShutdown(t *testing.T) {
	c := runtimeTestController(t, &runtimeTestPlatform{}, &controllerTestCDP{}, nil)
	queued := make(chan struct{})
	finished := make(chan struct{})
	go func() { <-queued; c.Refresh(); close(finished) }()
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	// Simulate user-owned state arriving after the controller released the path.
	// A delayed refresh must not overwrite it, even with otherwise valid packages.
	marker := []byte("fixture external state after shutdown\n")
	if err := os.WriteFile(c.configPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	close(queued)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("canceled queued refresh did not return")
	}
	if err := c.updatePackages(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled package refresh=%v", err)
	}
	contents, err := os.ReadFile(c.configPath)
	if err != nil || string(contents) != string(marker) {
		t.Fatalf("shutdown allowed a late state write: %q err=%v", contents, err)
	}
}
