package core

import (
	"context"
	"errors"
	"time"
)

const CodexBundleIdentifier = "com.openai.codex"

const (
	CodexCDPEndpoint   = "127.0.0.1:9335"
	CodexCDPOrigin     = "http://" + CodexCDPEndpoint
	CodexCDPTargetsURL = CodexCDPOrigin + "/json/list"
)

var CodexDebuggingArguments = []string{
	"--remote-debugging-address=127.0.0.1",
	"--remote-debugging-port=9335",
	"--remote-allow-origins=" + CodexCDPOrigin,
}

type CodexLaunchOptions struct {
	DisableGPUAcceleration bool
	Mode                   CodexLaunchMode
}

type CodexLaunchMode string

const (
	CodexLaunchNormal   CodexLaunchMode = "normal"
	CodexLaunchEnhanced CodexLaunchMode = "enhanced"
)

func codexLaunchArguments(options CodexLaunchOptions, operatingSystem string) []string {
	arguments := []string{}
	if options.Mode == CodexLaunchEnhanced {
		arguments = append(arguments, CodexDebuggingArguments...)
	}
	if !options.DisableGPUAcceleration {
		return arguments
	}
	if operatingSystem == "darwin" {
		arguments = append(arguments, "--use-gl=angle", "--use-angle=swiftshader")
	} else {
		arguments = append(arguments, "--disable-gpu")
	}
	return arguments
}

type Platform interface {
	IsCodexRunning(ctx context.Context) (bool, error)
	ObserveCodex(ctx context.Context) (CodexObservation, error)
	ActivateCodex(ctx context.Context) error
	LaunchCodex(ctx context.Context, options CodexLaunchOptions) error
	RestartCodex(ctx context.Context, options CodexLaunchOptions) error
	Architecture() string
}

var (
	ErrManualCodexExitRequired = errors.New("请先保存工作并从官方 Codex 正常退出，然后再启动增强模式")
	ErrCodexIdentityUnverified = errors.New("无法核验官方 Codex 进程及 CDP 监听归属")
	ErrCodexTargetChanged      = errors.New("官方 Codex 目标身份已变化，已停止连接")
	ErrCodexTargetExited       = errors.New("已核验的官方 Codex 进程已经正常退出")
)

// Process creation time is part of the identity: a reused PID is a new target.
type CodexProcessIdentity struct {
	ProcessID      uint32    `json:"processID"`
	ExecutablePath string    `json:"executablePath"`
	StartedAt      time.Time `json:"startedAt"`
	ApplicationID  string    `json:"applicationID"`
}

func (i CodexProcessIdentity) Valid() bool {
	return i.ProcessID != 0 && i.ExecutablePath != "" && !i.StartedAt.IsZero() && i.ApplicationID != ""
}

func (i CodexProcessIdentity) Equal(other CodexProcessIdentity) bool {
	return i.ProcessID == other.ProcessID && i.ExecutablePath == other.ExecutablePath &&
		i.StartedAt.Equal(other.StartedAt) && i.ApplicationID == other.ApplicationID
}

type CodexObservation struct {
	Running          bool                  `json:"running"`
	Target           *CodexProcessIdentity `json:"target,omitempty"`
	ListenerPresent  bool                  `json:"listenerPresent"`
	ListenerOwned    bool                  `json:"listenerOwned"`
	ListenerObserved bool                  `json:"listenerObserved"`
}

func verifyCodexTarget(ctx context.Context, platform Platform, expected CodexProcessIdentity) error {
	observation, err := platform.ObserveCodex(ctx)
	if err != nil {
		return err
	}
	if !observation.Running && observation.ListenerObserved {
		return ErrCodexTargetExited
	}
	if observation.Target == nil || !observation.Target.Equal(expected) {
		return ErrCodexTargetChanged
	}
	if !observation.ListenerOwned {
		return ErrCodexIdentityUnverified
	}
	return nil
}

// backgroundRepairPlatform is implemented by platforms that keep repairing state after the call that
// triggered them returned. Such work follows the application lifetime instead of a call context and
// reports its failures to the log.
type backgroundRepairPlatform interface {
	useBackgroundRepairContext(ctx context.Context, logger *Logger)
}
