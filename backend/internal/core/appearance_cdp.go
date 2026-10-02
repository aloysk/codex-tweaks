package core

import (
	"context"
	"errors"
)

func (s *CDPService) SetAppearance(ctx context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return AppearanceRuntimeResult{}, err
	}
	defer s.mu.Unlock()
	if s.stopped || request.Revision == 0 {
		return AppearanceRuntimeResult{}, ErrAppearanceUnavailable
	}
	settings, err := validateAppearanceSettings(request.Settings)
	if err != nil {
		return AppearanceRuntimeResult{}, err
	}
	if settings.ReadingLayout != "native" {
		return AppearanceRuntimeResult{}, ErrAppearanceUnsupportedLayout
	}
	request.Settings = settings
	// An unacknowledged dispatch still owns its attempted target. A cancel must
	// retire that exact page even if another official window appeared meanwhile.
	if request.TargetID == "" && s.appearanceTarget != nil && s.boundTarget != nil && s.boundTarget.Equal(s.appearanceTarget.identity) {
		request.TargetID = s.appearanceTarget.target.ID
	}
	targets, err := s.discoverTargets(ctx)
	if err != nil {
		return AppearanceRuntimeResult{}, err
	}
	var target CDPTarget
	if request.TargetID == "" {
		if len(targets) > 1 {
			return AppearanceRuntimeResult{}, ErrAppearanceAmbiguousTarget
		}
		if len(targets) != 1 {
			return AppearanceRuntimeResult{}, ErrAppearanceUnavailable
		}
		target = targets[0]
	} else {
		for _, candidate := range targets {
			if candidate.ID == request.TargetID {
				target = candidate
				break
			}
		}
		if target.ID == "" {
			return AppearanceRuntimeResult{}, ErrAppearanceUnavailable
		}
	}
	request.TargetID = target.ID
	if s.appearanceTarget != nil && s.appearanceTarget.target.ID != target.ID {
		if err := s.cleanupAppearanceBindingLocked(ctx); err != nil {
			return AppearanceRuntimeResult{}, err
		}
	}
	value, err := s.evaluate(ctx, appearanceProbeScript(s.owner, s.epoch, target.ID, request.Revision, settings), *target.WebSocketDebuggerURL)
	if err != nil {
		return AppearanceRuntimeResult{}, err
	}
	settingsKey := JSONLiteral(settings)
	confirmed := (value["status"] == "applied" || value["status"] == "native") && value["settingsKey"] == settingsKey
	result := AppearanceRuntimeResult{}
	if !confirmed {
		if value["status"] == "foreign" {
			return AppearanceRuntimeResult{}, ErrRendererOwnership
		}
		if value["status"] == "stopped" {
			return AppearanceRuntimeResult{}, context.Canceled
		}
		s.trackAttempt(target)
		// Record before dispatch: cancellation can close the WebSocket while a
		// browser evaluation is still queued. Cleanup must include that target.
		s.appearanceTarget = &attemptedAppearanceTarget{target: target, identity: *s.boundTarget, epoch: s.epoch}
		result = AppearanceRuntimeResult{TargetID: target.ID, Revision: request.Revision, Status: "pending"}
		value, err = s.evaluate(ctx, appearanceApplyScript(s.owner, s.epoch, request), *target.WebSocketDebuggerURL)
		if err != nil {
			return result, err
		}
	}
	status, _ := value["status"].(string)
	targetID, _ := value["targetID"].(string)
	revision, _ := value["revision"].(float64)
	if status == "unsupportedLayout" {
		return result, ErrAppearanceUnsupportedLayout
	}
	if status == "foreign" {
		return result, ErrRendererOwnership
	}
	if status == "stopped" {
		return result, context.Canceled
	}
	if (status != "applied" && status != "native") || targetID != target.ID || revision != float64(request.Revision) || value["settingsKey"] != settingsKey {
		return result, ErrAppearanceRenderer
	}
	return AppearanceRuntimeResult{TargetID: targetID, Revision: request.Revision, Status: status}, nil
}

type attemptedAppearanceTarget struct {
	target   CDPTarget
	identity CodexProcessIdentity
	epoch    uint64
}

// Changing a verified process binding must first retire the owned appearance
// from its original identity. Failure prevents silently adopting another page.
func (s *CDPService) cleanupAppearanceBindingLocked(ctx context.Context) error {
	if s.appearanceTarget == nil {
		return nil
	}
	attempt := s.appearanceTarget
	if s.verifyIdentity == nil {
		return ErrCodexIdentityUnverified
	}
	err := s.verifyIdentity(ctx, attempt.identity)
	if errors.Is(err, ErrCodexTargetExited) {
		s.appearanceTarget = nil
		return nil
	}
	if err != nil {
		return err
	}
	previous := s.boundTarget
	s.boundTarget = &attempt.identity
	defer func() { s.boundTarget = previous }()
	value, err := s.evaluate(ctx, appearanceCleanupScript(s.owner, attempt.epoch), *attempt.target.WebSocketDebuggerURL)
	if err != nil {
		return err
	}
	if value["status"] != "native" {
		return ErrAppearanceRenderer
	}
	s.appearanceTarget = nil
	return nil
}
