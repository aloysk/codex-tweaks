package core

import (
	"strings"
	"testing"
)

func TestAppearanceRendererScriptsParseBeforeAnyDOMWork(t *testing.T) {
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	request := AppearanceRuntimeRequest{Settings: settings, TargetID: "main", Revision: 2}
	source := ""
	for _, script := range []string{appearanceApplyScript(rendererFixtureOwner, 1, request), appearanceProbeScript(rendererFixtureOwner, 1, "main", 2, settings), appearanceCleanupScript(rendererFixtureOwner, 1)} {
		source += "new Function(" + JSONLiteral(script) + ");\n"
	}
	runRendererDOMFixture(t, source)
	if strings.Contains(appearanceApplyScript(rendererFixtureOwner, 1, request), "file:") {
		t.Fatal("filesystem URL crossed renderer boundary")
	}
}
