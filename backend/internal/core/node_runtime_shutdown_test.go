package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type nodeShutdownFixtureWriter struct{}

func (nodeShutdownFixtureWriter) Write(value []byte) (int, error) { return len(value), nil }
func (nodeShutdownFixtureWriter) Close() error                    { return nil }

func TestNodeStopAllUsesOneBudgetForEveryOwnedProcess(t *testing.T) {
	supervisor := NewNodeRuntimeSupervisor(nil, nil, nil)
	var canceled atomic.Int32
	for index := 0; index < 8; index++ {
		done := make(chan struct{})
		process := &nodeRuntimeProcess{stdin: nodeShutdownFixtureWriter{}, done: done, cancel: func() { canceled.Add(1); close(done) }}
		supervisor.processes[fmt.Sprintf("fixture-%d", index)] = process
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := supervisor.StopAllContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("node shutdown budget: elapsed=%s err=%v", time.Since(start), err)
	}
	deadline := time.Now().Add(time.Second)
	for canceled.Load() != 8 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if canceled.Load() != 8 {
		t.Fatalf("owned processes not canceled: %d/8", canceled.Load())
	}
	if len(supervisor.RunningPackageIDs()) != 0 {
		t.Fatal("stopped processes still published as running")
	}
}

func TestNodeStartFailureBodyIsNotPersisted(t *testing.T) {
	const canary = "a private package callback sentence without credential syntax"
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewNodeRuntimeSupervisor(nil, nil, logger)
	supervisor.starting["fixture"] = nodeRuntimeStart{AuthorizationID: "authorized-fixture"}
	supervisor.recordStartFailure("fixture", "authorized-fixture", canary)
	if supervisor.failures["fixture"].Message != canary {
		t.Fatal("in-memory failure detail was lost")
	}
	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview, canary) {
		t.Fatal("external callback body was persisted")
	}
}
