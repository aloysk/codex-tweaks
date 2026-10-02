//go:build windows

package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWindowsObservationPinsPathProcessCreationAndListenerOwner(t *testing.T) {
	identity := CodexProcessIdentity{ProcessID: 42, ExecutablePath: `C:\Program Files\WindowsApps\OpenAI.Codex_test\app\ChatGPT.exe`, StartedAt: time.Unix(1000, 0), ApplicationID: "OpenAI.Codex_2p2nqsd0c76g0!App"}
	for _, fixture := range []struct {
		name        string
		processes   []CodexProcessIdentity
		listenerID  uint32
		address     string
		unverified  int
		wantErr     bool
		wantRunning bool
		wantOwned   bool
	}{
		{"valid", []CodexProcessIdentity{identity}, 42, "127.0.0.1", 0, false, true, true},
		{"another listener", []CodexProcessIdentity{identity}, 99, "127.0.0.1", 0, false, true, false},
		{"non loopback", []CodexProcessIdentity{identity}, 42, "0.0.0.0", 0, false, true, false},
		{"multiple instances", []CodexProcessIdentity{identity, identity}, 42, "127.0.0.1", 0, true, true, false},
		{"unrelated name", nil, 99, "127.0.0.1", 1, false, true, false},
		{"exited", nil, 0, "", 0, false, false, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			listeners := []windowsListenerIdentity{}
			if fixture.listenerID != 0 {
				listeners = append(listeners, windowsListenerIdentity{ProcessID: fixture.listenerID, Address: fixture.address})
			}
			observation, err := validateWindowsCodexObservation(windowsProcessObservation{ExecutablePath: identity.ExecutablePath, ApplicationID: identity.ApplicationID, Processes: fixture.processes, Listeners: listeners, UnverifiedCount: fixture.unverified})
			if (err != nil) != fixture.wantErr || observation.Running != fixture.wantRunning || observation.ListenerOwned != fixture.wantOwned {
				t.Fatalf("observation=%#v err=%v", observation, err)
			}
			if fixture.name == "valid" && (observation.Target == nil || !observation.Target.StartedAt.Equal(identity.StartedAt)) {
				t.Fatal("process creation identity lost")
			}
		})
	}
}

func TestWindowsEnhancedLaunchPreservesRunningProcesses(t *testing.T) {
	calls := 0
	platform := &windowsPlatform{runner: windowsCommandRunnerFunc(func(_ context.Context, command string, args []string, _ string, _ []string) (CommandResult, error) {
		calls++
		t.Fatalf("process mutated: %s %v", command, args)
		return CommandResult{}, nil
	}), observeProcesses: func(context.Context) (windowsProcessObservation, error) {
		return windowsProcessObservation{UnverifiedCount: 1}, nil
	}}
	if err := platform.RestartCodex(context.Background(), CodexLaunchOptions{Mode: CodexLaunchEnhanced}); !errors.Is(err, ErrManualCodexExitRequired) {
		t.Fatalf("restart=%v", err)
	}
	if calls != 0 {
		t.Fatalf("unexpected mutation calls=%d", calls)
	}
}

func noRunningWindowsObservation(context.Context) (windowsProcessObservation, error) {
	return windowsProcessObservation{}, nil
}
