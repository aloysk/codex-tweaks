//go:build windows

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Only the registered Store package provides an attachable identity. An explicit
// executable remains a launch preference, not evidence of listener ownership.
const codexProcessObservationPowerShell = `$ErrorActionPreference = 'Stop'
$package = Get-AppxPackage -Name 'OpenAI.Codex' |
    Where-Object { $_.PackageFamilyName -eq 'OpenAI.Codex_2p2nqsd0c76g0' -and $_.SignatureKind -eq 'Store' } |
    Sort-Object Version -Descending | Select-Object -First 1
$path = ''
$identity = ''
if ($null -ne $package) {
    $application = ($package | Get-AppxPackageManifest).Package.Applications.Application |
        Where-Object { $_.Executable -match '(^|[\\/])ChatGPT[.]exe$' } | Select-Object -First 1
    if ($null -ne $application) {
        $path = [System.IO.Path]::GetFullPath((Join-Path $package.InstallLocation $application.Executable))
        $identity = $package.PackageFamilyName + '!' + $application.Id
    }
}
$processes = @()
$unverifiedCount = 0
$candidates = @(Get-CimInstance Win32_Process -Filter "Name = 'ChatGPT.exe'" |
    Where-Object { $_.CommandLine -notmatch '(^|\s)--type=' })
$unverifiedCount = @($candidates | Where-Object { !$_.CommandLine -or !$path -or $_.ExecutablePath -ne $path }).Count
if ($path) {
    $processes = @($candidates |
        Where-Object { $_.ExecutablePath -eq $path -and $_.CommandLine } |
        ForEach-Object { @{ processID = [uint32]$_.ProcessId; executablePath = $_.ExecutablePath;
            startedAt = $_.CreationDate.ToUniversalTime().ToString('o'); applicationID = $identity } })
}
$listeners = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { $_.LocalPort -eq 9335 } |
    ForEach-Object { @{ processID = [uint32]$_.OwningProcess; address = $_.LocalAddress } })
@{ executablePath = $path; applicationID = $identity; processes = $processes; listeners = $listeners; unverifiedCount = $unverifiedCount } |
    ConvertTo-Json -Compress -Depth 5`

type windowsProcessObservation struct {
	ExecutablePath  string                 `json:"executablePath"`
	ApplicationID   string                 `json:"applicationID"`
	UnverifiedCount int                    `json:"unverifiedCount"`
	Processes       []CodexProcessIdentity `json:"processes"`
	Listeners       []struct {
		ProcessID uint32 `json:"processID"`
		Address   string `json:"address"`
	} `json:"listeners"`
}

func (p *windowsPlatform) ObserveCodex(ctx context.Context) (CodexObservation, error) {
	result, err := p.runner.Run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", codexProcessObservationPowerShell}, "", environmentSlice(environmentMap()))
	if err != nil {
		return CodexObservation{}, err
	}
	if err := requireCommandSuccess(result, "核验 Codex 进程"); err != nil {
		return CodexObservation{}, err
	}
	return parseWindowsCodexObservation([]byte(strings.TrimPrefix(strings.TrimSpace(result.Output), "\ufeff")))
}

func parseWindowsCodexObservation(data []byte) (CodexObservation, error) {
	var source windowsProcessObservation
	if err := json.Unmarshal(data, &source); err != nil {
		return CodexObservation{}, fmt.Errorf("%w: invalid process observation", ErrCodexIdentityUnverified)
	}
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
