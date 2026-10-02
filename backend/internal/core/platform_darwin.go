//go:build darwin

package core

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"
)

type darwinPlatform struct{ runner CommandRunner }

func NewPlatform(runner CommandRunner) Platform {
	if runner == nil {
		runner = SystemCommandRunner{}
	}
	return &darwinPlatform{runner: runner}
}

func (p *darwinPlatform) IsCodexRunning(ctx context.Context) (bool, error) {
	result, err := p.runner.Run(
		ctx,
		"/usr/bin/lsappinfo",
		[]string{"find", "bundleID=" + CodexBundleIdentifier},
		"",
		environmentSlice(environmentMap()),
	)
	if err != nil {
		return false, err
	}
	if err := requireCommandSuccess(result, "观察 Codex"); err != nil {
		return false, err
	}
	return result.Status == 0 && strings.TrimSpace(result.Output) != "", nil
}

func (p *darwinPlatform) ObserveCodex(ctx context.Context) (CodexObservation, error) {
	// LaunchServices proves bundle presence, but not CDP listener ownership. Keep
	// attachment unavailable until a platform adapter can verify the full identity.
	running, err := p.IsCodexRunning(ctx)
	return CodexObservation{Running: running}, err
}

func (p *darwinPlatform) ActivateCodex(ctx context.Context) error {
	result, err := p.runner.Run(ctx, "/usr/bin/open", []string{"-b", CodexBundleIdentifier}, "", environmentSlice(environmentMap()))
	if err != nil {
		return err
	}
	return requireCommandSuccess(result, "激活 Codex")
}

func (p *darwinPlatform) LaunchCodex(ctx context.Context, options CodexLaunchOptions) error {
	if options.Mode == CodexLaunchEnhanced {
		return errors.Join(errors.ErrUnsupported, ErrCodexIdentityUnverified)
	}
	launchArguments := codexLaunchArguments(options, runtime.GOOS)
	arguments := []string{"-b", CodexBundleIdentifier}
	if options.Mode == CodexLaunchEnhanced {
		arguments = append([]string{"-n"}, arguments...)
	}
	if len(launchArguments) > 0 {
		arguments = append(arguments, "--args")
		arguments = append(arguments, launchArguments...)
	}
	result, err := p.runner.Run(ctx, "/usr/bin/open", arguments, "", environmentSlice(environmentMap()))
	if err != nil {
		return err
	}
	if result.Status == 0 {
		return nil
	}
	return errors.New("没有找到已注册的官方 Codex，或 Codex 启动失败。")
}

func (p *darwinPlatform) RestartCodex(ctx context.Context, options CodexLaunchOptions) error {
	if options.Mode == CodexLaunchEnhanced {
		return errors.Join(errors.ErrUnsupported, ErrCodexIdentityUnverified)
	}
	running, err := p.IsCodexRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return ErrManualCodexExitRequired
	}
	return p.LaunchCodex(ctx, options)
}

func (*darwinPlatform) Architecture() string { return runtime.GOARCH }

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
