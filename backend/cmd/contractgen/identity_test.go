package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codex-tweaks/codex-tweaks/backend/internal/core"
)

func TestGeneratedIdentityMatchesNativeAndPackagingContracts(t *testing.T) {
	files, err := generatedFiles("repo")
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string][]byte{}
	for _, file := range files {
		outputs[filepath.ToSlash(file.path)] = file.data
	}
	var identity applicationIdentity
	if err := json.Unmarshal(outputs["repo/contract/application-identity.json"], &identity); err != nil {
		t.Fatal(err)
	}
	if identity != distributionIdentity() {
		t.Fatalf("packaging identity differs from Go: %#v", identity)
	}
	for path, expected := range map[string][]string{
		"repo/app/Sources/GeneratedPresentationContract.swift": {
			"static let name = " + quotedString(core.ApplicationName),
			"static let bundleIdentifier = " + quotedString(core.ApplicationBundleIdentifier),
			"static let environmentPrefix = " + quotedString(core.ApplicationEnvironmentPrefix),
		},
		"repo/windows/CodexTweaks.Windows/Generated/PresentationContract.g.cs": {
			"Name = " + quotedString(core.ApplicationName),
			"BundleIdentifier = " + quotedString(core.ApplicationBundleIdentifier),
			"EnvironmentPrefix = " + quotedString(core.ApplicationEnvironmentPrefix),
		},
	} {
		for _, value := range expected {
			if !strings.Contains(string(outputs[path]), value) {
				t.Errorf("%s does not expose %s", path, value)
			}
		}
	}
}

func TestPlatformMetadataMatchesDistributionIdentity(t *testing.T) {
	project := string(repositoryFile(t, "project.yml"))
	for _, expected := range []string{
		"productName: " + core.ApplicationName + "\n",
		"PRODUCT_NAME: " + core.ApplicationName + "\n",
		"PRODUCT_BUNDLE_IDENTIFIER: " + core.ApplicationBundleIdentifier + "\n",
		"PRODUCT_BUNDLE_IDENTIFIER: " + core.ApplicationBundleIdentifier + ".debug\n",
	} {
		if !strings.Contains(project, expected) {
			t.Errorf("project.yml is missing %q", expected)
		}
	}
	var info struct {
		Dictionary struct {
			Values []struct {
				XMLName xml.Name
				Text    string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(repositoryFile(t, "app/Resources/Info.plist"), &info); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for index := 0; index+1 < len(info.Dictionary.Values); index += 2 {
		key, value := info.Dictionary.Values[index], info.Dictionary.Values[index+1]
		if key.XMLName.Local != "key" {
			t.Fatalf("unexpected plist entry: %s", key.XMLName.Local)
		}
		if value.XMLName.Local == "string" {
			values[key.Text] = strings.TrimSpace(value.Text)
		} else {
			values[key.Text] = value.XMLName.Local
		}
	}
	if values["CFBundleDisplayName"] != core.ApplicationName {
		t.Errorf("macOS display name = %q", values["CFBundleDisplayName"])
	}
	if !core.ApplicationUpdatesEnabled {
		if _, exists := values["SUFeedURL"]; exists {
			t.Error("disabled distribution must not carry an update feed")
		}
		if _, exists := values["SUPublicEDKey"]; exists || strings.Contains(project, "SPARKLE_PUBLIC_ED_KEY:") {
			t.Error("disabled distribution must not carry a signing key")
		}
		for _, key := range []string{"SUEnableAutomaticChecks", "SUAllowsAutomaticUpdates", "SUAutomaticallyUpdate"} {
			if values[key] != "false" {
				t.Errorf("%s = %q, want false", key, values[key])
			}
		}
	}
	var windowsManifest struct {
		Identity struct {
			Name string `xml:"name,attr"`
		} `xml:"assemblyIdentity"`
	}
	if err := xml.Unmarshal(repositoryFile(t, "windows/CodexTweaks.Windows/app.manifest"), &windowsManifest); err != nil {
		t.Fatal(err)
	}
	if windowsManifest.Identity.Name != core.ApplicationBundleIdentifier {
		t.Errorf("Windows manifest identity = %q", windowsManifest.Identity.Name)
	}
	var windowsProject struct {
		Groups []struct {
			Product string `xml:"Product"`
			Company string `xml:"Company"`
		} `xml:"PropertyGroup"`
	}
	if err := xml.Unmarshal(repositoryFile(t, "windows/CodexTweaks.Windows/CodexTweaks.Windows.csproj"), &windowsProject); err != nil {
		t.Fatal(err)
	}
	if len(windowsProject.Groups) == 0 || windowsProject.Groups[0].Product != core.ApplicationName || windowsProject.Groups[0].Company != core.ApplicationPublisher {
		t.Fatalf("Windows product metadata differs from the distribution identity: %#v", windowsProject.Groups)
	}
}

func TestDMGTemplateUsesDistinctInstallName(t *testing.T) {
	var template struct {
		Title      string `json:"title"`
		VolumeName string `json:"volume_name"`
		Contents   []struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(repositoryFile(t, "scripts/dmgbuild.json"), &template); err != nil {
		t.Fatal(err)
	}
	if template.Title != core.ApplicationName || template.VolumeName != core.ApplicationName {
		t.Fatalf("DMG volume identity differs: %#v", template)
	}
	found := false
	for _, entry := range template.Contents {
		if entry.Type == "file" {
			found = true
			if entry.Name != core.ApplicationName+".app" || entry.Path != "dist/"+core.ApplicationName+"-arm64.app" {
				t.Fatalf("DMG application entry could overwrite another product: %#v", entry)
			}
		}
	}
	if !found {
		t.Fatal("DMG template has no application entry")
	}
}

func repositoryFile(t *testing.T, path string) []byte {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository metadata")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(contents), "\r\n", "\n"))
}
