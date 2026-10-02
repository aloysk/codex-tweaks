package core

import "context"

// AppearanceSettings stores only companion-owned choices and an opaque owned
// asset ID. A source file path and image bytes never enter the public contract.
type AppearanceSettings struct {
	Theme          string  `json:"theme"`
	ReadingLayout  string  `json:"readingLayout"`
	BackgroundMode string  `json:"backgroundMode"`
	SolidColor     string  `json:"solidColor"`
	ImageAssetID   *string `json:"imageAssetId"`
	OverlayOpacity int     `json:"overlayOpacity"`
}

func DefaultAppearanceSettings() AppearanceSettings {
	return AppearanceSettings{Theme: "native", ReadingLayout: "native", BackgroundMode: "off", SolidColor: "#DEF3E5", OverlayOpacity: 88}
}

type AppearanceOption struct {
	Value     string `json:"value"`
	TextKey   string `json:"textKey"`
	Supported bool   `json:"supported"`
}

type AppearanceOptions struct {
	Themes          []AppearanceOption `json:"themes"`
	ReadingLayouts  []AppearanceOption `json:"readingLayouts"`
	BackgroundModes []AppearanceOption `json:"backgroundModes"`
	OverlayMinimum  int                `json:"overlayMinimum"`
	OverlayMaximum  int                `json:"overlayMaximum"`
}

type AppearanceActions struct {
	Preview       bool `json:"preview"`
	Apply         bool `json:"apply"`
	CancelPreview bool `json:"cancelPreview"`
	RestoreNative bool `json:"restoreNative"`
	ImportImage   bool `json:"importImage"`
}

type AppearanceSnapshot struct {
	Saved         AppearanceSettings  `json:"saved"`
	Preview       *AppearanceSettings `json:"preview"`
	Status        string              `json:"status"`
	StatusTextKey string              `json:"statusTextKey"`
	ErrorTextKey  *string             `json:"errorTextKey"`
	TargetID      *string             `json:"targetId"`
	Revision      uint64              `json:"revision"`
	Actions       AppearanceActions   `json:"actions"`
	Options       AppearanceOptions   `json:"options"`
}

type AppearanceImageResult struct {
	AssetID string `json:"assetId"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Format  string `json:"format"`
}

// AppearanceRuntime is deliberately optional: a fake CDPRuntime cannot silently
// fall through to a real listener just because it lacks appearance support.
type AppearanceRuntime interface {
	SetAppearance(context.Context, AppearanceRuntimeRequest) (AppearanceRuntimeResult, error)
}

type AppearanceRuntimeRequest struct {
	Settings AppearanceSettings
	DataURL  string
	TargetID string
	Revision uint64
}

type AppearanceRuntimeResult struct {
	TargetID string
	Revision uint64
	Status   string
}
