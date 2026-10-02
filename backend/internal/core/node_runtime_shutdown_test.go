package core

import (
	"context"
	"errors"
	"fmt"
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
