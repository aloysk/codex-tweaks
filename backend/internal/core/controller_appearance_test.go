package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type appearanceTestRuntime struct {
	controllerTestCDP
	mu       sync.Mutex
	requests []AppearanceRuntimeRequest
	apply    func(context.Context, AppearanceRuntimeRequest) (AppearanceRuntimeResult, error)
	cleanup  func(context.Context) (CDPCleanupResult, error)
	bind     func(context.Context, *CodexProcessIdentity) error
}

func (r *appearanceTestRuntime) BindTarget(ctx context.Context, identity *CodexProcessIdentity) error {
	if r.bind != nil {
		return r.bind(ctx, identity)
	}
	return nil
}

func (r *appearanceTestRuntime) Inject(context.Context, Payload, int) (CDPInjectionResult, error) {
	return CDPInjectionResult{TargetCount: 1, SuccessCount: 1, PackageErrors: map[string]string{}, TargetErrors: map[string]string{}}, nil
}
func (r *appearanceTestRuntime) SetAppearance(ctx context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	apply := r.apply
	r.mu.Unlock()
	if apply != nil {
		return apply(ctx, request)
	}
	status := "applied"
	if appearanceSettingsNative(request.Settings) {
		status = "native"
	}
	return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: status}, nil
}
func (r *appearanceTestRuntime) CleanupAllTargets(ctx context.Context) (CDPCleanupResult, error) {
	if r.cleanup != nil {
		return r.cleanup(ctx)
	}
	return CDPCleanupResult{TargetCount: 1, SuccessCount: 1, Targets: []CDPCleanupTargetResult{{TargetID: "main", Status: "cleaned"}}}, nil
}

func appearanceController(t *testing.T, runtime *appearanceTestRuntime) (*Controller, *runtimeTestPlatform) {
	t.Helper()
	identity := syntheticCodexIdentity()
	platform := &runtimeTestPlatform{observation: CodexObservation{Running: true, Target: &identity, ListenerObserved: true, ListenerPresent: true, ListenerOwned: true}}
	c := runtimeTestController(t, platform, runtime, nil)
	c.mu.Lock()
	next := c.config
	next.Enabled = true
	if err := c.persistConfigurationCandidateLocked(next); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.mu.Unlock()
	c.Refresh()
	if !c.Snapshot().Appearance.Actions.Preview {
		t.Fatal("verified runtime unavailable")
	}
	return c, platform
}

func TestControllerAppearancePreviewApplyCancelRestorePersistAfterConfirmation(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	defaults := c.Snapshot().Appearance.Saved
	settings := defaults
	settings.Theme = "mint"
	preview, err := c.PreviewAppearance(settings)
	if err != nil || preview.Status != "preview" || preview.Preview == nil || preview.Saved.Theme != "native" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	disk := AppConfiguration{}
	if err := readJSON(c.configPath, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Appearance.Theme != "native" {
		t.Fatal("preview persisted")
	}
	cancelled, err := c.CancelAppearancePreview()
	if err != nil || cancelled.Preview != nil || cancelled.Status != "native" {
		t.Fatalf("cancel=%#v err=%v", cancelled, err)
	}
	applied, err := c.ApplyAppearance(settings)
	if err != nil || applied.Status != "applied" || applied.Preview != nil || applied.Saved.Theme != "mint" {
		t.Fatalf("apply=%#v err=%v", applied, err)
	}
	if err := readJSON(c.configPath, &disk); err != nil || disk.Appearance.Theme != "mint" {
		t.Fatalf("disk=%#v err=%v", disk.Appearance, err)
	}
	restored, err := c.RestoreNativeAppearance()
	if err != nil || restored.Status != "native" || !reflect.DeepEqual(restored.Saved, defaults) {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.requests) != 4 {
		t.Fatalf("requests=%d", len(runtime.requests))
	}
	for index, request := range runtime.requests {
		if request.Revision != uint64(index+1) {
			t.Fatal("revision did not bind renderer confirmation")
		}
	}
}

func TestControllerAppearanceRecoveryRetiresUnacknowledgedRevision(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	if _, err := c.ApplyAppearance(mint); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.apply = func(_ context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		if request.Settings.Theme == "dark" {
			return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "pending"}, context.DeadlineExceeded
		}
		return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "applied"}, nil
	}
	runtime.mu.Unlock()
	dark := mint
	dark.Theme = "dark"
	if _, err := c.ApplyAppearance(dark); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost acknowledgement: %v", err)
	}
	pending := c.Snapshot().Appearance
	if pending.Status != "recoveryPending" {
		t.Fatalf("pending state: %#v", pending)
	}
	c.Refresh()
	recovered := c.Snapshot().Appearance
	runtime.mu.Lock()
	latest := runtime.requests[len(runtime.requests)-1]
	runtime.mu.Unlock()
	if latest.Settings.Theme != "mint" || latest.Revision <= pending.Revision || recovered.Revision != latest.Revision || recovered.Status != "applied" || recovered.Saved.Theme != "mint" {
		t.Fatalf("recovery reused a revision that a queued dark evaluation could still activate: latest=%#v state=%#v", latest, recovered)
	}
}

func TestControllerAppearancePersistenceFailureKeepsUnsavedPreviewAndRetries(t *testing.T) {
	c, _ := appearanceController(t, &appearanceTestRuntime{})
	original := c.configPath
	c.configPath = c.store.StateDirectory // atomic replacement cannot replace a directory
	settings := DefaultAppearanceSettings()
	settings.Theme = "dark"
	failed, err := c.ApplyAppearance(settings)
	if err == nil || failed.Status != "unsaved" || failed.Preview == nil || failed.Preview.Theme != "dark" || failed.Saved.Theme != "native" || failed.ErrorTextKey == nil || *failed.ErrorTextKey != "appearance.error.persistence" {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	c.configPath = original
	c.Refresh()
	if c.Snapshot().Appearance.Status != "unsaved" {
		t.Fatal("monitor claimed unsaved settings were saved")
	}
	applied, err := c.ApplyAppearance(settings)
	if err != nil || applied.Status != "applied" || applied.Saved.Theme != "dark" {
		t.Fatalf("retry=%#v err=%v", applied, err)
	}
}

func TestControllerAppearanceRejectsUnavailableLayoutAndFailedConfirmation(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	settings := DefaultAppearanceSettings()
	settings.ReadingLayout = "comfortable"
	if result, err := c.ApplyAppearance(settings); !errors.Is(err, ErrAppearanceUnsupportedLayout) || result.Status != "unsupported" || result.Saved.ReadingLayout != "native" {
		t.Fatalf("layout=%#v err=%v", result, err)
	}
	runtime.mu.Lock()
	runtime.apply = func(_ context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision + 1, Status: "applied"}, nil
	}
	runtime.mu.Unlock()
	settings = DefaultAppearanceSettings()
	settings.Theme = "mint"
	if result, err := c.ApplyAppearance(settings); !errors.Is(err, ErrAppearanceRenderer) || result.Saved.Theme != "native" {
		t.Fatalf("unconfirmed=%#v err=%v", result, err)
	}
	idle := runtimeTestController(t, &runtimeTestPlatform{}, &controllerTestCDP{}, nil)
	if idle.Snapshot().Appearance.Actions.Preview {
		t.Fatal("missing fake capability must not fall through to real CDP")
	}
	if _, err := idle.PreviewAppearance(settings); err == nil {
		t.Fatal("disabled runtime accepted appearance")
	}
}

func TestControllerAppearanceCancelInvalidatesLateApplyAndKeepsCancelAvailableWhileBusy(t *testing.T) {
	entered := make(chan struct{})
	released := make(chan struct{})
	runtime := &appearanceTestRuntime{apply: func(ctx context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		if request.Settings.Theme == "mint" {
			close(entered)
			<-ctx.Done()
			close(released)
			return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "applied"}, nil
		}
		return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "native"}, nil
	}}
	c, _ := appearanceController(t, runtime)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	result := make(chan error, 1)
	go func() { _, err := c.ApplyAppearance(settings); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("appearance did not start")
	}
	snapshot := c.Snapshot().Appearance
	if snapshot.Actions.Apply || !snapshot.Actions.CancelPreview {
		t.Fatal("busy apply must disable apply and retain cancellation")
	}
	cancelled, err := c.CancelAppearancePreview()
	if err != nil || cancelled.Status != "native" || cancelled.Preview != nil || cancelled.Saved.Theme != "native" {
		t.Fatalf("cancel=%#v err=%v", cancelled, err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt in-flight work")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("late accepted result=%v", err)
	}
	if c.Snapshot().Appearance.Status != "native" {
		t.Fatal("late result resurrected appearance")
	}
}

func TestControllerAppearanceDisableShutdownAndTargetChangeDiscardPreview(t *testing.T) {
	for _, action := range []string{"disable", "shutdown", "targetChange"} {
		t.Run(action, func(t *testing.T) {
			c, platform := appearanceController(t, &appearanceTestRuntime{})
			settings := DefaultAppearanceSettings()
			settings.Theme = "mint"
			if _, err := c.PreviewAppearance(settings); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "disable":
				if err := c.SetEnabled(false); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				if err := c.Shutdown(); err != nil {
					t.Fatal(err)
				}
			case "targetChange":
				identity := syntheticCodexIdentity()
				identity.StartedAt = identity.StartedAt.Add(time.Second)
				platform.setObservation(CodexObservation{Running: true, Target: &identity, ListenerObserved: true, ListenerPresent: true, ListenerOwned: true})
				c.Refresh()
			}
			if c.Snapshot().Appearance.Preview != nil || c.Snapshot().Appearance.Status == "preview" {
				t.Fatal("preview survived its target lifetime")
			}
		})
	}
}

func TestControllerAppearanceImageFailurePreservesSavedAndSourcePrivacy(t *testing.T) {
	c, _ := appearanceController(t, &appearanceTestRuntime{})
	path := filepath.Join(t.TempDir(), "private-image.svg")
	if err := os.WriteFile(path, []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot().Appearance.Saved
	if _, err := c.ImportAppearanceImage(path); err == nil {
		t.Fatal("SVG accepted")
	}
	if !reflect.DeepEqual(c.Snapshot().Appearance.Saved, before) {
		t.Fatal("failed import changed saved choices")
	}
	logBytes, _ := os.ReadFile(c.logger.Path)
	if strings.Contains(string(logBytes), path) {
		t.Fatal("private path entered diagnostics")
	}
}

func TestControllerAppearanceOwnedWallpaperSurvivesSourceMoveAndRestart(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, platform := appearanceController(t, runtime)
	source := filepath.Join(t.TempDir(), "user-selected.png")
	if err := os.WriteFile(source, appearancePNG(t, 2, 2, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	asset, err := c.ImportAppearanceImage(source)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultAppearanceSettings()
	settings.BackgroundMode = "local-image"
	settings.ImageAssetID = stringPointer(asset.AssetID)
	if snapshot, err := c.ApplyAppearance(settings); err != nil || snapshot.Saved.ImageAssetID == nil || *snapshot.Saved.ImageAssetID != asset.AssetID {
		t.Fatalf("apply=%#v err=%v", snapshot, err)
	}
	if err := os.Rename(source, source+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	secondRuntime := &appearanceTestRuntime{}
	restarted, err := newTestController(InitializeParams{ApplicationSupportDirectory: filepath.Dir(filepath.Dir(c.store.StateDirectory)), CacheDirectory: filepath.Join(t.TempDir(), "cache")}, nil, ControllerDependencies{Platform: platform, CDP: secondRuntime, DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown() })
	restarted.Refresh()
	if snapshot := restarted.Snapshot().Appearance; snapshot.Status != "applied" || snapshot.Saved.ImageAssetID == nil || *snapshot.Saved.ImageAssetID != asset.AssetID {
		t.Fatalf("restored=%#v", snapshot)
	}
	secondRuntime.mu.Lock()
	defer secondRuntime.mu.Unlock()
	if len(secondRuntime.requests) != 1 || !strings.HasPrefix(secondRuntime.requests[0].DataURL, "data:image/png;base64,") || strings.Contains(secondRuntime.requests[0].DataURL, source) {
		t.Fatal("saved wallpaper retained source dependency")
	}
}

func TestControllerAppearanceFailedCancelKeepsPreviewAndReportsRecovery(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	if _, err := c.PreviewAppearance(settings); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.apply = func(context.Context, AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		return AppearanceRuntimeResult{}, ErrAppearanceRenderer
	}
	runtime.mu.Unlock()
	snapshot, err := c.CancelAppearancePreview()
	if !errors.Is(err, ErrAppearanceRenderer) || snapshot.Preview == nil || snapshot.Preview.Theme != "mint" || snapshot.Saved.Theme != "native" || snapshot.Status != "recoveryPending" {
		t.Fatalf("cancel failure=%#v err=%v", snapshot, err)
	}
}

func TestControllerAppearanceListenerLossDiscardsPreviewButKeepsUnconfirmedCleanupTarget(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, platform := appearanceController(t, runtime)
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	if _, err := c.PreviewAppearance(settings); err != nil {
		t.Fatal(err)
	}
	identity := syntheticCodexIdentity()
	platform.setObservation(CodexObservation{Running: true, Target: &identity, ListenerObserved: true, ListenerPresent: true, ListenerOwned: false})
	runtime.bind = func(_ context.Context, identity *CodexProcessIdentity) error {
		if identity == nil {
			return ErrAppearanceRenderer
		}
		return nil
	}
	c.Refresh()
	state := c.Snapshot().Appearance
	if state.Preview != nil || state.Status != "recoveryPending" || state.TargetID == nil || *state.TargetID != "main" || state.Actions.Apply {
		t.Fatalf("unconfirmed loss=%#v", state)
	}
	runtime.bind = nil
	c.Refresh()
	state = c.Snapshot().Appearance
	if state.TargetID != nil || state.Preview != nil || state.Status == "recoveryPending" {
		t.Fatalf("confirmed detach=%#v", state)
	}
}

func TestControllerAppearanceCancelledCallerCannotAcquireNewRevision(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	before := c.Snapshot().Appearance
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	settings := DefaultAppearanceSettings()
	settings.Theme = "dark"
	if _, err := c.ApplyAppearanceContext(ctx, settings); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if after := c.Snapshot().Appearance; after.Revision != before.Revision || after.Saved.Theme != "native" || after.Actions.CancelPreview {
		t.Fatal("pre-start cancelled request mutated operation state")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.requests) != 0 {
		t.Fatal("cancelled request dispatched renderer work")
	}
}

func TestControllerAppearanceFirstDispatchFailureKeepsCancelAvailableUntilCleanupConfirmed(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	runtime.mu.Lock()
	runtime.apply = func(_ context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "pending"}, ErrAppearanceRenderer
	}
	runtime.mu.Unlock()
	settings := DefaultAppearanceSettings()
	settings.Theme = "dark"
	if _, err := c.PreviewAppearance(settings); !errors.Is(err, ErrAppearanceRenderer) {
		t.Fatal(err)
	}
	for _, refresh := range []bool{false, true} {
		if refresh {
			c.Refresh()
		}
		state := c.Snapshot().Appearance
		if state.Status != "recoveryPending" || state.TargetID == nil || *state.TargetID != "main" || state.Preview != nil || !state.Actions.CancelPreview || state.Saved.Theme != "native" {
			t.Fatalf("lost acknowledgement recovery=%#v afterRefresh=%v", state, refresh)
		}
	}
	runtime.mu.Lock()
	runtime.apply = nil
	runtime.mu.Unlock()
	state, err := c.CancelAppearancePreview()
	if err != nil || state.Status != "native" || state.Preview != nil || state.Actions.CancelPreview || state.Saved.Theme != "native" {
		t.Fatalf("confirmed cleanup=%#v err=%v", state, err)
	}
}

func TestControllerAppearanceCancelStopsPendingApplyBeforeSavedAssetRead(t *testing.T) {
	runtime := &appearanceTestRuntime{}
	c, _ := appearanceController(t, runtime)
	source := filepath.Join(t.TempDir(), "saved.png")
	if err := os.WriteFile(source, appearancePNG(t, 2, 2, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	asset, err := c.ImportAppearanceImage(source)
	if err != nil {
		t.Fatal(err)
	}
	saved := DefaultAppearanceSettings()
	saved.BackgroundMode = "local-image"
	saved.ImageAssetID = stringPointer(asset.AssetID)
	if _, err := c.ApplyAppearance(saved); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	revoked := make(chan struct{})
	barrier := make(chan struct{})
	runtime.mu.Lock()
	runtime.apply = func(ctx context.Context, request AppearanceRuntimeRequest) (AppearanceRuntimeResult, error) {
		if request.Settings.Theme == "dark" {
			close(started)
			<-ctx.Done()
			close(revoked)
			return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: "applied"}, nil
		}
		status := "applied"
		if appearanceSettingsNative(request.Settings) {
			status = "native"
			close(barrier)
		}
		return AppearanceRuntimeResult{TargetID: "main", Revision: request.Revision, Status: status}, nil
	}
	runtime.mu.Unlock()
	pending := make(chan error, 1)
	candidate := DefaultAppearanceSettings()
	candidate.Theme = "dark"
	go func() { _, err := c.ApplyAppearance(candidate); pending <- err }()
	<-started
	c.appearanceAssets.mu.Lock()
	cancelled := make(chan error, 1)
	go func() { _, err := c.CancelAppearancePreview(); cancelled <- err }()
	select {
	case <-revoked:
	case <-time.After(time.Second):
		c.appearanceAssets.mu.Unlock()
		t.Fatal("cancel waited for asset I/O before revoking apply")
	}
	select {
	case <-barrier:
	case <-time.After(time.Second):
		c.appearanceAssets.mu.Unlock()
		t.Fatal("cancel did not stop page candidate before asset I/O")
	}
	c.appearanceAssets.mu.Unlock()
	if err := <-pending; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	if state := c.Snapshot().Appearance; state.Saved.Theme != "native" || state.Saved.ImageAssetID == nil || *state.Saved.ImageAssetID != asset.AssetID || state.Preview != nil || state.Status != "applied" {
		t.Fatalf("stale restore=%#v", state)
	}
}
