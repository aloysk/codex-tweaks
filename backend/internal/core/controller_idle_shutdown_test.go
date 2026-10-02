package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type blockingShutdownObservationPlatform struct {
	idleControllerTestPlatform
	calls atomic.Int32
}

func (p *blockingShutdownObservationPlatform) ObserveCodex(ctx context.Context) (CodexObservation, error) {
	p.calls.Add(1)
	<-ctx.Done()
	return CodexObservation{}, ctx.Err()
}

func TestControllerIdleRefreshAndShutdownDoNotProbeOfficialProcesses(t *testing.T) {
	platform := &blockingShutdownObservationPlatform{}
	c := runtimeTestController(t, platform, &controllerTestCDP{}, nil)
	// The first refresh stops an empty runtime; the second exercises passive
	// recovery monitoring after that cleanup has already completed.
	c.Refresh()
	c.Refresh()
	started := time.Now()
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("idle shutdown was blocked: %s", elapsed)
	}
	if calls := platform.calls.Load(); calls != 0 {
		t.Fatalf("idle lifecycle observed unrelated processes %d times", calls)
	}
	got := c.Snapshot().Runtime.Recovery
	if !got.InjectionStopped || got.PageCleanup != "notNeeded" || got.DebugListener != "unknown" {
		t.Fatalf("idle recovery fabricated listener evidence: %#v", got)
	}
}

func TestControllerTargetShutdownPreservesCleanupFailureWhenObservationBlocks(t *testing.T) {
	platform := &blockingShutdownObservationPlatform{}
	cleanupFailure := errors.New("fixture page cleanup failed")
	runtime := &runtimeTestCDP{cleanup: func(context.Context) (CDPCleanupResult, error) {
		return CDPCleanupResult{TargetCount: 1, Targets: []CDPCleanupTargetResult{{TargetID: "main", Status: "pending", Error: cleanupFailure.Error()}}}, cleanupFailure
	}}
	c := runtimeTestController(t, platform, runtime, nil)
	identity := syntheticCodexIdentity()
	c.mu.Lock()
	c.runtime.Target = &identity
	c.runtime.Recovery.PageCleanup = "pending"
	c.mu.Unlock()
	started := time.Now()
	err := c.Shutdown()
	if !errors.Is(err, cleanupFailure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("target shutdown hid cleanup/observation failure: %v", err)
	}
	if elapsed := time.Since(started); elapsed > ShutdownTimeout+500*time.Millisecond {
		t.Fatalf("target shutdown exceeded its shared budget: %s", elapsed)
	}
	if platform.calls.Load() != 1 {
		t.Fatalf("existing target was not observed: %d", platform.calls.Load())
	}
	got := c.Snapshot().Runtime.Recovery
	if got.PageCleanup != "pending" || got.DebugListener != "unknown" {
		t.Fatalf("blocked observation confirmed recovery: %#v", got)
	}
}

func TestControllerAttemptedTargetCleanupFailureIsNotTreatedAsIdle(t *testing.T) {
	identity := syntheticCodexIdentity()
	platform := &runtimeTestPlatform{observation: CodexObservation{Running: true, Target: &identity, ListenerPresent: true, ListenerOwned: true, ListenerObserved: true}}
	cleanupFailure := errors.New("fixture unacknowledged target cleanup failed")
	runtime := &runtimeTestCDP{cleanup: func(context.Context) (CDPCleanupResult, error) {
		return CDPCleanupResult{TargetCount: 1, Targets: []CDPCleanupTargetResult{{TargetID: "attempt", Status: "pending", Error: cleanupFailure.Error()}}}, cleanupFailure
	}}
	c := runtimeTestController(t, platform, runtime, nil)
	// The CDP attempt can survive a cleared foreground-target observation.
	if err := c.Shutdown(); !errors.Is(err, cleanupFailure) {
		t.Fatalf("attempted target failure was ignored: %v", err)
	}
	got := c.Snapshot()
	if got.Status.Kind != StatusRecoveryPending || got.Runtime.Recovery.PageCleanup != "pending" || got.Runtime.Recovery.DebugListener != "open" {
		t.Fatalf("attempted target was treated as idle: %#v", got.Runtime.Recovery)
	}
}
