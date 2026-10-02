//go:build windows

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type windowsProcessObservation struct {
	ExecutablePath  string                    `json:"executablePath"`
	ApplicationID   string                    `json:"applicationID"`
	UnverifiedCount int                       `json:"unverifiedCount"`
	Processes       []CodexProcessIdentity    `json:"processes"`
	Listeners       []windowsListenerIdentity `json:"listeners"`
}

func (p *windowsPlatform) ObserveCodex(ctx context.Context) (CodexObservation, error) {
	if err := ctx.Err(); err != nil {
		return CodexObservation{}, err
	}
	if p.observeProcesses == nil {
		return CodexObservation{}, ErrCodexIdentityUnverified
	}
	source, err := p.observeProcesses(ctx)
	if err != nil {
		return CodexObservation{}, err
	}
	return validateWindowsCodexObservation(source)
}

func validateWindowsCodexObservation(source windowsProcessObservation) (CodexObservation, error) {
	result := CodexObservation{Running: len(source.Processes) > 0 || source.UnverifiedCount > 0, ListenerPresent: len(source.Listeners) > 0, ListenerObserved: true}
	if len(source.Processes) == 0 {
		return result, nil
	}
	if len(source.Processes) != 1 {
		return result, fmt.Errorf("%w: multiple official processes", ErrCodexIdentityUnverified)
	}
	target := source.Processes[0]
	if !target.Valid() || !filepath.IsAbs(target.ExecutablePath) || !strings.EqualFold(filepath.Clean(target.ExecutablePath), filepath.Clean(source.ExecutablePath)) ||
		target.ApplicationID != source.ApplicationID || !strings.HasPrefix(target.ApplicationID, "OpenAI.Codex_2p2nqsd0c76g0!") ||
		target.StartedAt.After(time.Now().Add(time.Minute)) {
		return result, ErrCodexIdentityUnverified
	}
	target.ExecutablePath = strings.ToLower(filepath.Clean(target.ExecutablePath))
	result.Target = &target
	result.ListenerOwned = len(source.Listeners) > 0
	for _, listener := range source.Listeners {
		if listener.ProcessID != target.ProcessID || listener.Address != "127.0.0.1" {
			result.ListenerOwned = false
		}
	}
	return result, nil
}
