package core

import (
	"errors"
	"regexp"
	"strings"
)

var (
	appearanceColorPattern         = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	appearanceAssetPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ErrAppearanceUnavailable       = errors.New("appearance capability is unavailable")
	ErrAppearanceAmbiguousTarget   = errors.New("appearance target is ambiguous")
	ErrAppearanceUnsupportedLayout = errors.New("reading layout is not supported by this renderer")
	ErrAppearanceRenderer          = errors.New("renderer did not confirm appearance")
)

type appearanceValidationError struct{ key string }

func (e *appearanceValidationError) Error() string { return e.key }
func appearanceError(key string) error {
	return &appearanceValidationError{key: "appearance.error." + key}
}

func validateAppearanceSettings(settings AppearanceSettings) (AppearanceSettings, error) {
	if !containsString([]string{"native", "mint", "dark"}, settings.Theme) ||
		!containsString([]string{"native", "comfortable", "compact"}, settings.ReadingLayout) ||
		!containsString([]string{"off", "solid", "local-image"}, settings.BackgroundMode) ||
		!appearanceColorPattern.MatchString(settings.SolidColor) || settings.OverlayOpacity < 50 || settings.OverlayOpacity > 100 {
		return settings, appearanceError("invalidSettings")
	}
	if settings.ImageAssetID != nil && !appearanceAssetPattern.MatchString(*settings.ImageAssetID) {
		return settings, appearanceError("assetUnavailable")
	}
	if settings.BackgroundMode == "local-image" && settings.ImageAssetID == nil {
		return settings, appearanceError("assetUnavailable")
	}
	settings.SolidColor = strings.ToUpper(settings.SolidColor)
	return cloneAppearanceSettings(settings), nil
}

func cloneAppearanceSettings(value AppearanceSettings) AppearanceSettings {
	value.ImageAssetID = cloneStringPointer(value.ImageAssetID)
	return value
}

func appearanceSettingsNative(value AppearanceSettings) bool {
	return value.Theme == "native" && value.ReadingLayout == "native" && value.BackgroundMode == "off"
}

func appearanceOptions() AppearanceOptions {
	return AppearanceOptions{
		Themes:          []AppearanceOption{{"native", "appearance.theme.native", true}, {"mint", "appearance.theme.mint", true}, {"dark", "appearance.theme.dark", true}},
		ReadingLayouts:  []AppearanceOption{{"native", "appearance.layout.native", true}, {"comfortable", "appearance.layout.comfortable", false}, {"compact", "appearance.layout.compact", false}},
		BackgroundModes: []AppearanceOption{{"off", "appearance.background.off", true}, {"solid", "appearance.background.solid", true}, {"local-image", "appearance.background.localImage", true}},
		OverlayMinimum:  50, OverlayMaximum: 100,
	}
}

func appearanceErrorTextKey(err error) string {
	var validation *appearanceValidationError
	if errors.As(err, &validation) {
		return validation.key
	}
	switch {
	case errors.Is(err, ErrAppearanceUnavailable), errors.Is(err, ErrCDPEndpointUnavailable), errors.Is(err, ErrCodexIdentityUnverified):
		return "appearance.error.unavailable"
	case errors.Is(err, ErrAppearanceAmbiguousTarget):
		return "appearance.error.ambiguousTarget"
	case errors.Is(err, ErrAppearanceUnsupportedLayout):
		return "appearance.error.unsupportedLayout"
	default:
		return "appearance.error.renderer"
	}
}
