package core

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRejectedConfigurationDoesNotChangeMemoryOrPreventRetry(t *testing.T) {
	root := t.TempDir()
	c, err := newTestController(InitializeParams{
		ApplicationSupportDirectory: filepath.Join(root, "support"),
		CacheDirectory:              filepath.Join(root, "cache"),
		PreferredLanguages:          []string{"en"},
	}, nil, ControllerDependencies{DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	path := c.configPath
	before := c.Snapshot()
	c.configPath = root // A directory cannot be replaced by a configuration file.
	if err := c.SetDisableGPUAcceleration(!before.DisableGPUAcceleration); err == nil {
		t.Fatal("failed persistence was accepted")
	}
	if c.Snapshot().DisableGPUAcceleration != before.DisableGPUAcceleration {
		t.Fatal("failed persistence changed GPU preference")
	}
	if err := c.SetLanguage(LanguageJapanese); err == nil {
		t.Fatal("language save failure was accepted")
	}
	if c.Snapshot().Presentation.LanguagePreference != before.Presentation.LanguagePreference {
		t.Fatal("failed persistence changed language preference")
	}
	c.configPath = path
	if err := c.SetDisableGPUAcceleration(!before.DisableGPUAcceleration); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLanguage(LanguageJapanese); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().DisableGPUAcceleration == before.DisableGPUAcceleration || c.Snapshot().Presentation.LanguagePreference != "ja" {
		t.Fatal("successful retry did not publish saved preferences")
	}
}

func TestUnconfiguredUpdatePreferencesAreRejected(t *testing.T) {
	root := t.TempDir()
	c, err := newTestController(InitializeParams{
		ApplicationSupportDirectory: filepath.Join(root, "support"), CacheDirectory: filepath.Join(root, "cache"),
	}, nil, ControllerDependencies{DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	if err := c.SetUpdateAutoCheck(true); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("auto check: %v", err)
	}
	if err := c.SetUpdateChannel(UpdateBeta); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("channel: %v", err)
	}
	if err := c.SetUpdateAutoCheck(false); err != nil {
		t.Fatal(err)
	}
}

func TestFailedPackageDiscoveryDoesNotPublishConfiguration(t *testing.T) {
	root := t.TempDir()
	c, err := newTestController(InitializeParams{
		ApplicationSupportDirectory: filepath.Join(root, "support"), CacheDirectory: filepath.Join(root, "cache"),
	}, nil, ControllerDependencies{DisableBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	path := c.configPath
	makeStorePackage(t, c.store, "discovered", "discovered", "1.0.0", 0, nil, "")
	c.configPath = root
	if err := c.updatePackages(); err == nil {
		t.Fatal("failed discovery persistence was accepted")
	}
	if len(c.packages) != 0 || containsString(c.config.DisabledPackageIDs, "discovered") || c.disabledPackageIDs["discovered"] {
		t.Fatal("failed persistence published discovered package state")
	}
	if c.config.KnownPackageIDs != nil && containsString(*c.config.KnownPackageIDs, "discovered") {
		t.Fatal("failed persistence published known package IDs")
	}
	c.configPath = path
	if err := c.updatePackages(); err != nil {
		t.Fatal(err)
	}
	if len(c.packages) != 1 || !c.disabledPackageIDs["discovered"] {
		t.Fatal("retry did not discover a disabled package")
	}
}
