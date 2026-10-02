package core

// Distribution identity is independent of upstream's package API and source
// namespaces. Changing these values after release requires a migration plan.
const (
	ApplicationName              = "Codex Tweaks Companion"
	ApplicationBundleIdentifier  = "io.github.aloysk.codexcompanion"
	ApplicationPublisher         = "aloysk"
	ApplicationArtifactPrefix    = "Codex-Tweaks-Companion"
	ApplicationEnvironmentPrefix = "CODEX_COMPANION_"
	UpdateRepository             = "aloysk/codex-tweaks"
	UpdateRepositoryURL          = "https://github.com/" + UpdateRepository

	// Distribution signing and update feeds must be configured and verified
	// before either native updater can be enabled.
	ApplicationUpdatesEnabled = false
)
