package core

import (
	"context"
	"errors"
	"time"
)

func (c *Controller) appearanceSnapshotLocked() AppearanceSnapshot {
	status := c.appearanceStatus
	if status == "" {
		status = "native"
	}
	_, supported := c.cdp.(AppearanceRuntime)
	available := supported && c.config.Enabled && c.runtime.AttachSupported && c.runtime.Target != nil && c.status.Kind == StatusConnected && c.status.TargetCount > 0 && !c.shuttingDown && !c.appearanceBusy
	if !available && status == "native" && !appearanceSettingsNative(c.config.Appearance) {
		status = "unavailable"
	}
	errorKey := cloneStringPointer(c.appearanceErrorKey)
	if !supported || c.config.Enabled && !c.runtime.AttachSupported {
		if status == "native" {
			status = "unavailable"
		}
		if errorKey == nil {
			errorKey = stringPointer("appearance.error.unavailable")
		}
	}
	var preview *AppearanceSettings
	if c.appearancePreview != nil {
		copy := cloneAppearanceSettings(*c.appearancePreview)
		preview = &copy
	}
	var target *string
	if c.appearanceTargetID != "" {
		target = stringPointer(c.appearanceTargetID)
	}
	return AppearanceSnapshot{
		Saved: cloneAppearanceSettings(c.config.Appearance), Preview: preview,
		Status: status, StatusTextKey: "appearance.status." + status, ErrorTextKey: errorKey,
		TargetID: target, Revision: c.appearanceRevision, Options: appearanceOptions(),
		Actions: AppearanceActions{Preview: available, Apply: available, CancelPreview: (preview != nil || c.appearanceBusy || status == "recoveryPending" && target != nil) && !c.shuttingDown,
			RestoreNative: !c.shuttingDown && !c.appearanceBusy && (available || !appearanceSettingsNative(c.config.Appearance) || preview != nil), ImportImage: !c.shuttingDown && !c.appearanceBusy},
	}
}

func (c *Controller) appearanceSnapshot() AppearanceSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appearanceSnapshotLocked()
}

func (c *Controller) ImportAppearanceImage(path string) (AppearanceImageResult, error) {
	c.mu.Lock()
	stopped := c.shuttingDown
	c.mu.Unlock()
	if stopped {
		return AppearanceImageResult{}, ErrAppearanceUnavailable
	}
	result, err := c.appearanceAssets.importImage(path)
	if err != nil {
		c.mu.Lock()
		c.appearanceErrorKey = stringPointer(appearanceErrorTextKey(err))
		c.mu.Unlock()
		c.logger.Warn("外观图片导入失败：class=" + diagnosticErrorClass(err))
		c.emit()
	} else {
		c.mu.Lock()
		c.appearanceErrorKey = nil
		c.mu.Unlock()
		c.emit()
	}
	return result, err
}

func (c *Controller) PreviewAppearance(settings AppearanceSettings) (AppearanceSnapshot, error) {
	return c.PreviewAppearanceContext(context.Background(), settings)
}

func (c *Controller) ApplyAppearance(settings AppearanceSettings) (AppearanceSnapshot, error) {
	return c.ApplyAppearanceContext(context.Background(), settings)
}

func (c *Controller) PreviewAppearanceContext(ctx context.Context, settings AppearanceSettings) (AppearanceSnapshot, error) {
	return c.changeAppearance(ctx, settings, false, false)
}

func (c *Controller) ApplyAppearanceContext(ctx context.Context, settings AppearanceSettings) (AppearanceSnapshot, error) {
	return c.changeAppearance(ctx, settings, true, false)
}

func (c *Controller) CancelAppearancePreview() (AppearanceSnapshot, error) {
	return c.changeAppearance(context.Background(), AppearanceSettings{}, false, true)
}

func (c *Controller) RestoreNativeAppearance() (AppearanceSnapshot, error) {
	return c.RestoreNativeAppearanceContext(context.Background())
}

func (c *Controller) RestoreNativeAppearanceContext(ctx context.Context) (AppearanceSnapshot, error) {
	return c.changeAppearance(ctx, DefaultAppearanceSettings(), true, true)
}

func (c *Controller) changeAppearance(caller context.Context, candidate AppearanceSettings, persist, restoring bool) (AppearanceSnapshot, error) {
	// The transport may revoke a reserved request before its goroutine starts.
	// Do not let that request acquire a newer revision after a completed cancel.
	if err := caller.Err(); err != nil {
		return c.appearanceSnapshot(), err
	}
	isCancel := restoring && !persist
	settings := candidate
	if !isCancel {
		validated, err := validateAppearanceSettings(candidate)
		if err == nil && validated.ReadingLayout != "native" {
			err = ErrAppearanceUnsupportedLayout
		}
		if err != nil {
			c.mu.Lock()
			c.appearanceErrorKey = stringPointer(appearanceErrorTextKey(err))
			if errors.Is(err, ErrAppearanceUnsupportedLayout) {
				c.appearanceStatus = "unsupported"
			}
			c.mu.Unlock()
			c.emit()
			return c.appearanceSnapshot(), err
		}
		settings = validated
	}
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	stopCaller := context.AfterFunc(caller, cancel)
	defer stopCaller()
	c.mu.Lock()
	if err := caller.Err(); err != nil {
		c.mu.Unlock()
		return c.appearanceSnapshot(), err
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return c.appearanceSnapshot(), err
	}
	if c.shuttingDown {
		c.mu.Unlock()
		return c.appearanceSnapshot(), ErrAppearanceUnavailable
	}
	if c.appearanceCancel != nil {
		c.appearanceCancel()
	}
	// Cancel linearizes before slow asset reads and before an older request can
	// commit. Its restore candidate is captured at this same operation boundary.
	if isCancel {
		settings = cloneAppearanceSettings(c.config.Appearance)
	}
	c.appearanceRevision++
	revision, epoch := c.appearanceRevision, c.runtimeEpoch
	c.appearanceCancel = cancel
	c.appearanceBusy = true
	c.appearanceErrorKey = nil
	targetID := c.appearanceTargetID
	c.mu.Unlock()
	c.emit()
	defer func() {
		c.mu.Lock()
		if c.appearanceRevision == revision {
			c.appearanceBusy = false
			c.appearanceCancel = nil
		}
		c.mu.Unlock()
		c.emit()
	}()
	if err := lockWithContext(ctx, &c.runtimeMu); err != nil {
		return c.appearanceFailure(revision, err)
	}
	defer c.runtimeMu.Unlock()
	if err := caller.Err(); err != nil {
		return c.appearanceFailure(revision, err)
	}
	if err := ctx.Err(); err != nil {
		return c.appearanceFailure(revision, err)
	}
	c.mu.Lock()
	// A confirmed global stop is also confirmation that no appearance effect
	// remains. This permits restoring the saved choice while disconnected.
	alreadyClean := restoring && appearanceSettingsNative(settings) && c.disabledCleanupCompleted && (c.runtime.Recovery.PageCleanup == "confirmed" || c.runtime.Recovery.PageCleanup == "notNeeded")
	valid := c.appearanceRevision == revision && c.runtimeEpoch == epoch && !c.shuttingDown
	enabled := c.config.Enabled
	c.mu.Unlock()
	if !valid {
		return c.appearanceFailure(revision, context.Canceled)
	}
	if !enabled && !alreadyClean {
		return c.appearanceFailure(revision, ErrAppearanceUnavailable)
	}
	result := AppearanceRuntimeResult{Revision: revision, Status: "native"}
	if !alreadyClean {
		runtime, ok := c.cdp.(AppearanceRuntime)
		if !ok {
			return c.appearanceFailure(revision, ErrAppearanceUnavailable)
		}
		observation, observeErr := c.platform.ObserveCodex(ctx)
		c.mu.Lock()
		identity := c.runtime.Target
		verified := observation.Target != nil && identity != nil && identity.Equal(*observation.Target) && observation.ListenerOwned
		c.mu.Unlock()
		if observeErr != nil || !verified {
			return c.appearanceFailure(revision, ErrAppearanceUnavailable)
		}
		apply := func(value AppearanceSettings, dataURL string) (AppearanceRuntimeResult, error) {
			response, err := runtime.SetAppearance(ctx, AppearanceRuntimeRequest{Settings: value, DataURL: dataURL, TargetID: targetID, Revision: revision})
			c.recordAppearanceAttempt(revision, epoch, response)
			if response.TargetID != "" {
				targetID = response.TargetID
			}
			if err != nil {
				return response, err
			}
			if response.Revision != revision || response.TargetID == "" || response.Status != "applied" && response.Status != "native" {
				return response, ErrAppearanceRenderer
			}
			return response, nil
		}
		// Establish the newer page watermark before reading a saved wallpaper.
		// Even a missing/slow asset then cannot let the cancelled candidate wake up.
		if isCancel {
			var err error
			result, err = apply(DefaultAppearanceSettings(), "")
			if err != nil {
				return c.appearanceFailure(revision, err)
			}
		}
		if !isCancel || !appearanceSettingsNative(settings) {
			var dataURL string
			var err error
			if settings.BackgroundMode == "local-image" {
				dataURL, err = c.appearanceAssets.dataURLContext(ctx, *settings.ImageAssetID)
			}
			if err != nil {
				return c.appearanceFailure(revision, err)
			}
			if err := caller.Err(); err != nil {
				return c.appearanceFailure(revision, err)
			}
			if err := ctx.Err(); err != nil {
				return c.appearanceFailure(revision, err)
			}
			result, err = apply(settings, dataURL)
			if err != nil {
				return c.appearanceFailure(revision, err)
			}
		}
	}
	c.mu.Lock()
	if c.appearanceRevision != revision || c.runtimeEpoch != epoch || c.shuttingDown || ctx.Err() != nil || caller.Err() != nil {
		c.mu.Unlock()
		return c.appearanceSnapshot(), context.Canceled
	}
	c.appearanceTargetID = result.TargetID
	copy := cloneAppearanceSettings(settings)
	c.appearancePreview = &copy
	c.appearanceStatus = "preview"
	if persist {
		next := c.config
		next.Appearance = cloneAppearanceSettings(settings)
		if err := c.persistConfigurationCandidateLocked(next); err != nil {
			c.appearanceStatus = "unsaved"
			c.appearanceErrorKey = stringPointer("appearance.error.persistence")
			c.mu.Unlock()
			c.logger.Warn("外观设置保存失败：class=" + diagnosticErrorClass(err))
			return c.appearanceSnapshot(), err
		}
		c.appearancePreview = nil
		c.appearanceStatus = "applied"
		if appearanceSettingsNative(settings) {
			c.appearanceStatus = "native"
		}
	} else if restoring {
		c.appearancePreview = nil
		c.appearanceStatus = "applied"
		if appearanceSettingsNative(settings) {
			c.appearanceStatus = "native"
		}
	}
	c.appearanceBusy = false
	c.mu.Unlock()
	return c.appearanceSnapshot(), nil
}

func (c *Controller) recordAppearanceAttempt(revision, epoch uint64, result AppearanceRuntimeResult) {
	if result.TargetID == "" || result.Revision != revision {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appearanceRevision == revision && c.runtimeEpoch == epoch && !c.shuttingDown {
		c.appearanceTargetID = result.TargetID
	}
}

func (c *Controller) appearanceFailure(revision uint64, err error) (AppearanceSnapshot, error) {
	c.mu.Lock()
	if c.appearanceRevision == revision && !c.shuttingDown {
		c.appearanceBusy = false
		c.appearanceErrorKey = stringPointer(appearanceErrorTextKey(err))
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.appearanceErrorKey = stringPointer("appearance.error.cancelled")
		}
		c.appearanceStatus = "unavailable"
		if c.appearanceTargetID != "" {
			c.appearanceStatus = "recoveryPending"
		}
		if errors.Is(err, ErrAppearanceUnsupportedLayout) {
			c.appearanceStatus = "unsupported"
		}
	}
	c.mu.Unlock()
	c.logger.Warn("外观操作未确认：class=" + diagnosticErrorClass(err))
	return c.appearanceSnapshot(), err
}

// Call under c.mu before invalidating a target or global enhancement epoch.
func (c *Controller) invalidateAppearanceLocked() {
	if c.appearanceCancel != nil {
		c.appearanceCancel()
		c.appearanceCancel = nil
	}
	c.appearanceRevision++
	c.appearanceBusy = false
	c.appearancePreview = nil
	c.appearanceErrorKey = nil
	c.appearanceStatus = "unavailable"
}

func (c *Controller) suspendAppearanceForDetach() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appearanceTargetID != "" || c.appearancePreview != nil || c.appearanceBusy {
		c.invalidateAppearanceLocked()
	}
}

func (c *Controller) completeAppearanceDetach(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.appearanceTargetID != "" {
			c.appearanceStatus = "recoveryPending"
			c.appearanceErrorKey = stringPointer("appearance.error.renderer")
		}
		return
	}
	c.appearanceTargetID = ""
	c.appearanceStatus = "native"
	if !appearanceSettingsNative(c.config.Appearance) {
		c.appearanceStatus = "unavailable"
	}
}

// Refresh already holds runtimeMu. It reapplies saved settings after a renderer
// replacement without changing any package's payload version or force counter.
func (c *Controller) refreshAppearance(ctx context.Context, epoch uint64) {
	runtime, ok := c.cdp.(AppearanceRuntime)
	if !ok {
		return
	}
	c.mu.Lock()
	if c.runtimeEpoch != epoch || !c.config.Enabled || c.shuttingDown || c.appearanceBusy {
		c.mu.Unlock()
		return
	}
	settings := cloneAppearanceSettings(c.config.Appearance)
	if c.appearancePreview != nil {
		settings = cloneAppearanceSettings(*c.appearancePreview)
	}
	if appearanceSettingsNative(settings) && c.appearancePreview == nil && c.appearanceTargetID == "" {
		c.mu.Unlock()
		return
	}
	if c.appearanceRevision == 0 {
		c.appearanceRevision = 1
	} else if c.appearanceStatus == "recoveryPending" {
		// A lost response can leave the entire old evaluation queued. A
		// recovery is a new request, so that evaluation cannot activate later.
		c.appearanceRevision++
	}
	revision, targetID := c.appearanceRevision, c.appearanceTargetID
	c.mu.Unlock()
	var dataURL string
	var err error
	if settings.BackgroundMode == "local-image" {
		dataURL, err = c.appearanceAssets.dataURLContext(ctx, *settings.ImageAssetID)
	}
	var result AppearanceRuntimeResult
	if err == nil {
		result, err = runtime.SetAppearance(ctx, AppearanceRuntimeRequest{Settings: settings, DataURL: dataURL, TargetID: targetID, Revision: revision})
	}
	c.recordAppearanceAttempt(revision, epoch, result)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runtimeEpoch != epoch || c.appearanceRevision != revision || c.shuttingDown || c.appearanceBusy {
		return
	}
	if err != nil || result.Revision != revision || result.TargetID == "" || result.Status != "applied" && result.Status != "native" {
		if err == nil {
			err = ErrAppearanceRenderer
		}
		c.appearanceStatus = "unavailable"
		if c.appearanceTargetID != "" {
			c.appearanceStatus = "recoveryPending"
		}
		c.appearanceErrorKey = stringPointer(appearanceErrorTextKey(err))
		return
	}
	c.appearanceTargetID = result.TargetID
	if c.appearancePreview == nil {
		c.appearanceStatus = "applied"
		if appearanceSettingsNative(settings) {
			c.appearanceStatus = "native"
		}
	}
}
