package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/codex-tweaks/codex-tweaks/backend/internal/core"
)

type appearanceRPCPlatform struct {
	isolatedPlatform
	identity core.CodexProcessIdentity
}

func (p appearanceRPCPlatform) ObserveCodex(context.Context) (core.CodexObservation, error) {
	identity := p.identity
	return core.CodexObservation{Running: true, Target: &identity, ListenerObserved: true, ListenerPresent: true, ListenerOwned: true}, nil
}

type appearanceRPCRuntime struct {
	isolatedCDP
	started    chan struct{}
	blockTheme string
	blockOnce  bool
	blocked    sync.Once
}

func (*appearanceRPCRuntime) Inject(context.Context, core.Payload, int) (core.CDPInjectionResult, error) {
	return core.CDPInjectionResult{TargetCount: 1, SuccessCount: 1}, nil
}
func (r *appearanceRPCRuntime) SetAppearance(ctx context.Context, request core.AppearanceRuntimeRequest) (core.AppearanceRuntimeResult, error) {
	theme := r.blockTheme
	if theme == "" {
		theme = "mint"
	}
	shouldBlock := request.Settings.Theme == theme
	if shouldBlock && r.blockOnce {
		shouldBlock = false
		r.blocked.Do(func() { shouldBlock = true })
	}
	if shouldBlock {
		select {
		case r.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return core.AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision}, ctx.Err()
	}
	status := "native"
	if request.Settings.Theme != "native" {
		status = "applied"
	}
	return core.AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: status}, nil
}

type appearanceRPCFrame struct {
	response decodedResponse
	err      error
}
type appearanceRPCSession struct {
	input    *io.PipeWriter
	frames   chan appearanceRPCFrame
	finished chan error
	pending  map[int64]decodedResponse
	done     bool
}

func newAppearanceRPCSession(t *testing.T, runtime core.CDPRuntime, connected bool) *appearanceRPCSession {
	t.Helper()
	root := t.TempDir()
	input, peer := io.Pipe()
	output, writer := io.Pipe()
	session := &appearanceRPCSession{input: peer, frames: make(chan appearanceRPCFrame, 32), finished: make(chan error, 1), pending: map[int64]decodedResponse{}}
	var platform core.Platform = isolatedPlatform{}
	if connected {
		platform = appearanceRPCPlatform{identity: core.CodexProcessIdentity{ProcessID: 42, ExecutablePath: filepath.Join(root, "official", "ChatGPT.exe"), StartedAt: time.Now().Add(-time.Minute), ApplicationID: "synthetic-official"}}
	}
	server := NewServerWithDependencies(input, writer, core.ControllerDependencies{DisableBackground: true, Platform: platform, CDP: runtime})
	go func() { session.finished <- server.Serve(); _ = writer.Close(); _ = input.Close() }()
	go func() {
		defer close(session.frames)
		defer output.Close()
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var frame decodedResponse
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				session.frames <- appearanceRPCFrame{err: err}
				return
			}
			if frame.ID != 0 {
				session.frames <- appearanceRPCFrame{response: frame}
			}
		}
		if err := scanner.Err(); err != nil {
			session.frames <- appearanceRPCFrame{err: err}
		}
	}()
	t.Cleanup(func() {
		_ = peer.Close()
		if !session.done {
			select {
			case err := <-session.finished:
				if err != nil {
					t.Errorf("serve cleanup: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Error("RPC workers did not drain on EOF")
			}
		}
	})
	session.send(t, 1, "initialize", map[string]any{"applicationSupportDirectory": filepath.Join(root, "support"), "cacheDirectory": filepath.Join(root, "cache"), "currentVersion": "0.0.1-dev", "buildNumber": "1"})
	if frame := session.await(t, 1); frame.Error != nil {
		t.Fatalf("initialize: %#v", frame.Error)
	}
	if connected {
		session.send(t, 10, "setEnabled", map[string]bool{"enabled": true})
		if frame := session.await(t, 10); frame.Error != nil {
			t.Fatalf("set enabled: %#v", frame.Error)
		}
		// Enable schedules its isolated refresh asynchronously. A native page
		// cannot submit appearance changes before Go exposes the action either.
		deadline := time.Now().Add(3 * time.Second)
		for id := int64(11); ; id++ {
			session.send(t, id, "getState", nil)
			frame := session.await(t, id)
			appearance := frame.Result["appearance"].(map[string]any)
			if appearance["actions"].(map[string]any)["preview"] == true {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("synthetic connection did not expose appearance: %#v", frame.Result["status"])
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return session
}

func (s *appearanceRPCSession) send(t *testing.T, id int64, method string, params any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(s.input, string(data)); err != nil {
		t.Fatal(err)
	}
}
func (s *appearanceRPCSession) await(t *testing.T, id int64) decodedResponse {
	t.Helper()
	if frame, found := s.pending[id]; found {
		delete(s.pending, id)
		return frame
	}
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case frame, open := <-s.frames:
			if !open {
				t.Fatalf("RPC output closed before response %d", id)
			}
			if frame.err != nil {
				t.Fatalf("RPC frame: %v", frame.err)
			}
			if int64(frame.response.ID) == id {
				return frame.response
			}
			s.pending[int64(frame.response.ID)] = frame.response
		case <-deadline.C:
			t.Fatalf("RPC response %d timed out", id)
		}
	}
}
func (s *appearanceRPCSession) finish(t *testing.T) {
	t.Helper()
	select {
	case err := <-s.finished:
		s.done = true
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("RPC workers did not drain")
	}
}

func TestAppearanceRPCPendingOperationCanBeCanceledBeforeSaving(t *testing.T) {
	for _, method := range []string{"appearance.apply", "appearance.preview"} {
		t.Run(method, func(t *testing.T) {
			runtime := &appearanceRPCRuntime{started: make(chan struct{}, 1)}
			session := newAppearanceRPCSession(t, runtime, true)
			settings := core.DefaultAppearanceSettings()
			settings.Theme = "mint"
			session.send(t, 2, method, map[string]any{"settings": settings})
			select {
			case <-runtime.started:
			case <-time.After(time.Second):
				t.Fatal("renderer operation did not start")
			}
			// A second preview/apply cannot create an unbounded queue or win a
			// scheduling race over the cancellation reserved on the scanner.
			session.send(t, 3, "appearance.apply", map[string]any{"settings": settings})
			if frame := session.await(t, 3); frame.Error == nil {
				t.Fatal("concurrent appearance operation accepted")
			}
			started := time.Now()
			session.send(t, 4, "appearance.cancelPreview", nil)
			if frame := session.await(t, 4); frame.Error != nil {
				t.Fatalf("cancel: %#v", frame.Error)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("cancel waited for the five-second operation deadline: %s", elapsed)
			}
			if frame := session.await(t, 2); frame.Error == nil || frame.Error.Code != "cancelled" {
				t.Fatalf("pending request outcome: %#v", frame)
			}
			session.send(t, 5, "getState", nil)
			appearance := session.await(t, 5).Result["appearance"].(map[string]any)
			if appearance["saved"].(map[string]any)["theme"] != "native" || appearance["preview"] != nil {
				t.Fatalf("cancel persisted or retained the draft: %#v", appearance)
			}
			session.send(t, 6, "shutdown", nil)
			if frame := session.await(t, 6); frame.Error != nil {
				t.Fatalf("shutdown: %#v", frame.Error)
			}
			session.finish(t)
		})
	}
}

func TestAppearanceRPCPendingOperationDrainsOnShutdownAndEOF(t *testing.T) {
	for _, ending := range []string{"shutdown", "EOF"} {
		t.Run(ending, func(t *testing.T) {
			runtime := &appearanceRPCRuntime{started: make(chan struct{}, 1)}
			session := newAppearanceRPCSession(t, runtime, true)
			settings := core.DefaultAppearanceSettings()
			settings.Theme = "mint"
			session.send(t, 2, "appearance.apply", map[string]any{"settings": settings})
			select {
			case <-runtime.started:
			case <-time.After(time.Second):
				t.Fatal("renderer operation did not start")
			}
			if ending == "EOF" {
				_ = session.input.Close()
			} else {
				session.send(t, 3, "shutdown", nil)
				if frame := session.await(t, 3); frame.Error != nil {
					t.Fatalf("shutdown: %#v", frame.Error)
				}
			}
			if frame := session.await(t, 2); frame.Error == nil {
				t.Fatal("terminated request reported success")
			}
			session.finish(t)
		})
	}
}

func TestAppearanceRPCCancelRestoreRetainsPreviousSavedTheme(t *testing.T) {
	runtime := &appearanceRPCRuntime{started: make(chan struct{}, 1), blockTheme: "native", blockOnce: true}
	session := newAppearanceRPCSession(t, runtime, true)
	settings := core.DefaultAppearanceSettings()
	settings.Theme = "mint"
	session.send(t, 2, "appearance.apply", map[string]any{"settings": settings})
	if frame := session.await(t, 2); frame.Error != nil {
		t.Fatalf("save baseline: %#v", frame.Error)
	}
	session.send(t, 3, "appearance.restoreNative", nil)
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("restore operation did not start")
	}
	started := time.Now()
	session.send(t, 4, "appearance.cancelPreview", nil)
	if frame := session.await(t, 4); frame.Error != nil {
		t.Fatalf("cancel restore: %#v", frame.Error)
	}
	if time.Since(started) > time.Second {
		t.Fatal("restore blocked cancellation until its deadline")
	}
	if frame := session.await(t, 3); frame.Error == nil || frame.Error.Code != "cancelled" {
		t.Fatalf("restore outcome: %#v", frame)
	}
	session.send(t, 5, "getState", nil)
	appearance := session.await(t, 5).Result["appearance"].(map[string]any)
	if appearance["saved"].(map[string]any)["theme"] != "mint" || appearance["preview"] != nil {
		t.Fatalf("canceled restore replaced the saved theme: %#v", appearance)
	}
	session.send(t, 6, "shutdown", nil)
	if frame := session.await(t, 6); frame.Error != nil {
		t.Fatalf("shutdown: %#v", frame.Error)
	}
	session.finish(t)
}
