package core

import (
	"context"
	"errors"
	"strconv"
	"time"
)

func intString(value int) string         { return strconv.Itoa(value) }
func stringPointer(value string) *string { return &value }

func (c *Controller) CheckNodeEnvironment() {
	c.mu.Lock()
	if c.checkingNode {
		c.mu.Unlock()
		return
	}
	c.checkingNode = true
	c.mu.Unlock()
	c.emit()
	go func() {
		environment := c.builder.DetectNodeEnvironment(c.ctx)
		c.mu.Lock()
		c.nodeEnvironment = environment
		c.checkingNode = false
		c.mu.Unlock()
		if environment != nil {
			c.logger.Info("已检测到 Node.js " + environment.Version)
			c.scheduleDeveloperBuilds()
		} else {
			c.logger.Error("未找到可用的 Node.js、npm 和 npx")
		}
		c.emit()
	}()
}

func (c *Controller) CheckGitEnvironment() {
	c.mu.Lock()
	if c.checkingGit {
		c.mu.Unlock()
		return
	}
	c.checkingGit = true
	c.mu.Unlock()
	c.emit()
	go func() {
		environment := c.remote.DetectGitEnvironment(c.ctx)
		c.mu.Lock()
		c.gitEnvironment = environment
		c.checkingGit = false
		c.mu.Unlock()
		if environment != nil {
			c.logger.Info("已检测到 " + environment.Version)
			c.CheckManagedPackageUpdates(true)
		} else {
			c.logger.Error("未找到可用的 Git")
		}
		c.emit()
	}()
}

func (c *Controller) Refresh() {
	if c.ctx.Err() != nil {
		return
	}
	if !c.refreshMu.TryLock() {
		return
	}
	defer c.refreshMu.Unlock()
	if c.ctx.Err() != nil {
		return
	}
	if err := c.updatePackages(); err != nil {
		if c.ctx.Err() != nil {
			return
		}
		message := "无法读取功能包：" + err.Error()
		c.setStatus(AppStatus{Kind: StatusError, Message: &message})
		return
	}
	if c.ctx.Err() != nil {
		return
	}
	c.scheduleDeveloperBuilds()
	c.CheckManagedPackageUpdates(true)
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if lockWithContext(ctx, &c.runtimeMu) != nil {
		return
	}
	defer c.runtimeMu.Unlock()
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return
	}
	enabled := c.config.Enabled
	cleanupCompleted := c.disabledCleanupCompleted
	epoch := c.runtimeEpoch
	c.injectCancel = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.runtimeEpoch == epoch {
			c.injectCancel = nil
		}
		c.mu.Unlock()
	}()
	if !enabled {
		if !cleanupCompleted {
			cleanupContext, cleanupCancel := context.WithTimeout(c.ctx, ShutdownTimeout)
			_ = c.stopRuntimeLocked(cleanupContext)
			cleanupCancel()
		} else {
			c.observeRecoveryListener(ctx)
		}
		c.emit()
		return
	}

	observation, err := c.platform.ObserveCodex(ctx)
	if err != nil {
		c.cancelNodeActivity()
		c.suspendAppearanceForDetach()
		c.completeAppearanceDetach(c.cdp.BindTarget(ctx, nil))
		if c.nodeRuntime != nil {
			_ = c.nodeRuntime.StopAllContext(ctx)
		}
		c.publishRuntimeStatus(epoch, AppStatus{Kind: StatusError, Message: stringPointer(err.Error())})
		return
	}
	c.mu.Lock()
	if c.runtimeEpoch != epoch || !c.config.Enabled || c.shuttingDown || ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	targetChanged := c.runtime.Target != nil && (observation.Target == nil || !c.runtime.Target.Equal(*observation.Target))
	if targetChanged {
		c.invalidateAppearanceLocked()
		c.signals = signalsState{}
		if c.nodeCancel != nil {
			c.nodeCancel()
		}
	}
	c.runtime.Target = observation.Target
	c.runtime.AttachSupported = observation.Target != nil
	c.mu.Unlock()
	if !observation.Running {
		c.cancelNodeActivity()
		c.suspendAppearanceForDetach()
		c.completeAppearanceDetach(c.cdp.BindTarget(ctx, nil))
		if c.nodeRuntime != nil {
			_ = c.nodeRuntime.StopAllContext(ctx)
		}
		c.publishRuntimeStatus(epoch, AppStatus{Kind: StatusCodexNotRunning})
		return
	}
	if observation.Target == nil {
		c.cancelNodeActivity()
		c.suspendAppearanceForDetach()
		c.completeAppearanceDetach(c.cdp.BindTarget(ctx, nil))
		if c.nodeRuntime != nil {
			_ = c.nodeRuntime.StopAllContext(ctx)
		}
		c.publishRuntimeStatus(epoch, AppStatus{Kind: StatusRestartRequired, Message: stringPointer(ErrCodexIdentityUnverified.Error())})
		return
	}
	if !observation.ListenerOwned {
		c.cancelNodeActivity()
		c.suspendAppearanceForDetach()
		c.completeAppearanceDetach(c.cdp.BindTarget(ctx, nil))
		if c.nodeRuntime != nil {
			_ = c.nodeRuntime.StopAllContext(ctx)
		}
		c.publishRuntimeStatus(epoch, AppStatus{Kind: StatusRestartRequired})
		return
	}
	if err := c.cdp.BindTarget(ctx, observation.Target); err != nil {
		c.cancelNodeActivity()
		if targetChanged {
			c.completeAppearanceDetach(err)
		}
		c.publishRuntimeStatus(epoch, AppStatus{Kind: StatusError, Message: stringPointer(err.Error())})
		return
	}
	if targetChanged {
		c.completeAppearanceDetach(nil)
	}
	c.mu.Lock()
	if c.runtimeEpoch != epoch || !c.config.Enabled || c.shuttingDown || ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	if c.nodeLifetime == nil || c.nodeLifetime.Err() != nil {
		c.nodeLifetime, c.nodeCancel = context.WithCancel(c.ctx)
	}
	nodeLifetime := c.nodeLifetime
	c.disabledCleanupCompleted = false
	packages := append([]Package(nil), c.packages...)
	disabled := cloneSet(c.disabledPackageIDs)
	trust := c.nodeTrustByPackageIDLocked()
	nodeEnvironment := cloneNodeEnvironment(c.nodeEnvironment)
	forceGeneration := c.forceGeneration
	c.runtime.Recovery = RecoverySnapshot{PageCleanup: "pending", DebugListener: "open", Targets: []CDPCleanupTargetResult{}}
	c.mu.Unlock()
	if c.nodeRuntime != nil {
		c.nodeRuntime.Reconcile(nodeLifetime, packages, disabled, trust, nodeEnvironment)
	}
	nodeRunnable := map[string]bool{}
	if c.nodeRuntime != nil {
		for packageID := range c.nodeRuntime.RunningPackageIDs() {
			if trust[packageID] != "" {
				nodeRunnable[packageID] = true
			}
		}
	}
	loadResult := c.store.LoadPayload(packages, disabled, nodeRunnable)
	result, err := c.cdp.Inject(ctx, loadResult.Payload, forceGeneration)
	if err != nil {
		c.cancelNodeActivity()
	}
	if err != nil || result.SuccessCount == 0 {
		c.suspendAppearanceForDetach()
		c.completeAppearanceDetach(c.cdp.BindTarget(ctx, nil))
	}
	c.mu.Lock()
	if c.shuttingDown || !c.config.Enabled || c.runtimeEpoch != epoch {
		c.mu.Unlock()
		return
	}
	c.packagePayloadErrors = loadResult.PackageErrors
	combined := map[string]string{}
	if c.nodeRuntime != nil {
		combined = c.nodeRuntime.RuntimeErrors()
	}
	for packageID, message := range result.PackageErrors {
		combined[packageID] = message
	}
	c.packageRuntimeErrors = combined
	switch {
	case errors.Is(err, ErrCDPEndpointUnavailable):
		c.status = AppStatus{Kind: StatusRestartRequired}
	case err != nil:
		c.status = AppStatus{Kind: StatusError, Message: stringPointer(err.Error())}
	case result.TargetCount == 0:
		c.status = AppStatus{Kind: StatusWaitingForPage}
	case result.SuccessCount > 0:
		c.status = AppStatus{Kind: StatusConnected, TargetCount: result.SuccessCount}
	default:
		c.status = AppStatus{Kind: StatusError, Message: stringPointer("页面没有确认增强结果")}
	}
	if c.status.Kind != StatusConnected {
		c.signals = signalsState{}
	}
	c.mu.Unlock()
	if err == nil && result.SuccessCount > 0 {
		c.refreshAppearance(ctx, epoch)
		c.refreshSignals(ctx, epoch)
	}
	c.emit()
}

func (c *Controller) cancelNodeActivity() {
	c.mu.Lock()
	if c.nodeCancel != nil {
		c.nodeCancel()
	}
	c.mu.Unlock()
}

func (c *Controller) observeRecoveryListener(ctx context.Context) {
	c.mu.Lock()
	if c.runtime.Target == nil && len(c.runtime.Recovery.Targets) == 0 {
		c.runtime.Recovery.DebugListener = "unknown"
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	observation, err := c.platform.ObserveCodex(ctx)
	listener := "unknown"
	if err == nil && observation.ListenerObserved {
		if observation.ListenerPresent {
			listener = "open"
		} else {
			listener = "closed"
		}
	}
	c.mu.Lock()
	c.runtime.Recovery.DebugListener = listener
	c.mu.Unlock()
}

func (c *Controller) publishRuntimeStatus(epoch uint64, status AppStatus) {
	c.mu.Lock()
	if c.runtimeEpoch != epoch || !c.config.Enabled || c.shuttingDown {
		c.mu.Unlock()
		return
	}
	c.status = status
	if status.Kind != StatusConnected {
		c.signals = signalsState{}
	}
	c.mu.Unlock()
	c.emit()
}

func (c *Controller) stopRuntimeLocked(ctx context.Context) error {
	nodeResults := make(chan error, 1)
	go func() {
		if c.nodeRuntime != nil {
			nodeResults <- c.nodeRuntime.StopAllContext(ctx)
		} else {
			nodeResults <- nil
		}
	}()
	result, cleanupError := c.cdp.CleanupAllTargets(ctx)
	recovery := RecoverySnapshot{InjectionStopped: true, PageCleanup: "pending", DebugListener: "unknown", Targets: result.Targets}
	if cleanupError == nil && result.Complete() {
		recovery.PageCleanup = "confirmed"
		if result.TargetCount == 0 {
			recovery.PageCleanup = "notNeeded"
		}
	}
	if err := lockWithContext(ctx, &c.mu); err != nil {
		return errors.Join(cleanupError, err)
	}
	hasTarget := c.runtime.Target != nil || len(c.runtime.Recovery.Targets) > 0 || result.TargetCount > 0
	c.mu.Unlock()
	// An idle companion has no debug listener of its own to verify. Starting an
	// unrelated platform probe here can exhaust the entire shutdown budget.
	if hasTarget {
		observation, observationError := c.platform.ObserveCodex(ctx)
		if observationError == nil && observation.ListenerObserved {
			if observation.ListenerPresent {
				recovery.DebugListener = "open"
			} else {
				recovery.DebugListener = "closed"
			}
		}
	}
	var nodeError error
	select {
	case nodeError = <-nodeResults:
	case <-ctx.Done():
		nodeError = ctx.Err()
	}
	failure := errors.Join(nodeError, cleanupError)
	if err := lockWithContext(ctx, &c.mu); err != nil {
		return errors.Join(failure, err)
	}
	c.runtime.Recovery = recovery
	c.disabledCleanupCompleted = failure == nil && result.Complete()
	if c.disabledCleanupCompleted {
		c.appearanceTargetID = ""
		c.appearancePreview = nil
		c.appearanceStatus = "native"
		if !appearanceSettingsNative(c.config.Appearance) {
			c.appearanceStatus = "unavailable"
		}
	} else if c.appearanceTargetID != "" {
		c.appearanceStatus = "recoveryPending"
	}
	c.packageRuntimeErrors = map[string]string{}
	if failure != nil {
		c.status = AppStatus{Kind: StatusRecoveryPending, Message: stringPointer(failure.Error())}
	} else {
		c.status = AppStatus{Kind: StatusDisabled}
	}
	c.mu.Unlock()
	return failure
}

func (c *Controller) setStatus(status AppStatus) {
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
	c.emit()
}

func (c *Controller) OpenCodex() error {
	return c.launchCodex(CodexLaunchNormal)
}

func (c *Controller) launchCodex(mode CodexLaunchMode) error {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if err := lockWithContext(ctx, &c.runtimeMu); err != nil {
		return err
	}
	defer c.runtimeMu.Unlock()
	observation, err := c.platform.ObserveCodex(ctx)
	if err != nil {
		return err
	}
	if observation.Running {
		if mode == CodexLaunchEnhanced {
			return ErrManualCodexExitRequired
		}
		return c.platform.ActivateCodex(ctx)
	}
	c.setStatus(AppStatus{Kind: StatusLaunchingCodex})
	if err := c.platform.LaunchCodex(ctx, c.codexLaunchOptions(mode)); err != nil {
		c.setStatus(AppStatus{Kind: StatusError, Message: stringPointer(err.Error())})
		return err
	}
	c.logger.Info("用户已启动 Codex（" + string(mode) + "）")
	c.setStatus(AppStatus{Kind: StatusWaitingForCDP})
	return nil
}

// This action is retained in the protocol as an explicit enhanced launch. It
// never exits or kills the official app; an existing instance requires manual exit.
func (c *Controller) RestartCodex() error { return c.launchCodex(CodexLaunchEnhanced) }

func (c *Controller) RestartCodexUI() error { return ErrRendererReloadUnsafe }

func (c *Controller) Reinject() {
	c.mu.Lock()
	c.forceGeneration++
	c.mu.Unlock()
	go c.Refresh()
}

func (c *Controller) scheduleDeveloperBuilds() {
	c.mu.Lock()
	if !c.config.DeveloperMode || c.nodeEnvironment == nil {
		c.mu.Unlock()
		return
	}
	candidates := []Package{}
	for _, pkg := range c.packages {
		if c.disabledPackageIDs[pkg.ID] || c.buildingPackageIDs[pkg.ID] {
			continue
		}
		disposition := pkg.BuildDisposition(CompilerVersion)
		mayBuild := disposition == BuildSourceChanged || disposition == BuildNotBuilt && (pkg.Manifest == nil || len(pkg.Manifest.Dependencies) == 0)
		key, hasKey := pkg.BuildRequestKey(CompilerVersion)
		if mayBuild && hasKey && c.developerBuildAttemptKeys[pkg.ID] != key {
			c.developerBuildAttemptKeys[pkg.ID] = key
			candidates = append(candidates, pkg)
		}
	}
	c.mu.Unlock()
	for _, pkg := range candidates {
		c.startPackageBuild(pkg, false, false, true)
	}
}

func (c *Controller) BuildPackage(packageID string) error {
	pkg, exists := c.packageByID(packageID)
	if !exists {
		return errors.New("没有找到功能包：" + packageID)
	}
	c.startPackageBuild(pkg, true, true, false)
	return nil
}

func (c *Controller) startPackageBuild(pkg Package, installDependencies, allowCompilerDownload, automatic bool) {
	if pkg.ValidationError != nil {
		return
	}
	c.mu.Lock()
	if c.buildingPackageIDs[pkg.ID] || len(c.deletingPackageIDs) > 0 {
		c.mu.Unlock()
		return
	}
	c.buildingPackageIDs[pkg.ID] = true
	delete(c.packageBuildErrors, pkg.ID)
	delete(c.packageBuildErrorRequestKeys, pkg.ID)
	requestKey, hasRequestKey := pkg.BuildRequestKey(CompilerVersion)
	c.mu.Unlock()
	if automatic {
		c.logger.Info("自动编译功能包：" + pkg.DisplayName())
	} else {
		c.logger.Info("手动更新功能包：" + pkg.DisplayName())
	}
	c.emit()
	go func() {
		_, err := c.builder.Build(c.ctx, pkg, installDependencies, allowCompilerDownload)
		c.mu.Lock()
		delete(c.buildingPackageIDs, pkg.ID)
		if err != nil {
			c.packageBuildErrors[pkg.ID] = err.Error()
			if hasRequestKey {
				c.packageBuildErrorRequestKeys[pkg.ID] = requestKey
			}
		}
		c.mu.Unlock()
		if err != nil {
			c.logger.Error("功能包 " + pkg.DisplayName() + " 编译失败：class=" + diagnosticErrorClass(err))
			c.emit()
			return
		}
		if err := c.updatePackages(); err != nil {
			c.logger.Error("编译后重新读取功能包失败：" + err.Error())
		}
		c.mu.Lock()
		c.forceGeneration++
		c.mu.Unlock()
		c.logger.Info("功能包已编译并激活：" + pkg.DisplayName())
		c.emit()
		c.Refresh()
	}()
}

func stringMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
