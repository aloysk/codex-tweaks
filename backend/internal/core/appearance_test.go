package core

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appearancePNG(t *testing.T, width, height int, shade uint8) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for index := range picture.Pix {
		picture.Pix[index] = shade
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestAppearanceSettingsValidateBeforeAnyEffects(t *testing.T) {
	defaults := DefaultAppearanceSettings()
	if defaults.Theme != "native" || defaults.ReadingLayout != "native" || defaults.BackgroundMode != "off" || defaults.SolidColor != "#DEF3E5" || defaults.OverlayOpacity != 88 || defaults.ImageAssetID != nil {
		t.Fatal(defaults)
	}
	for _, edit := range []func(*AppearanceSettings){
		func(s *AppearanceSettings) { s.Theme = "auto" }, func(s *AppearanceSettings) { s.ReadingLayout = "hidden" },
		func(s *AppearanceSettings) { s.BackgroundMode = "remote" }, func(s *AppearanceSettings) { s.SolidColor = "red;display:none" },
		func(s *AppearanceSettings) { s.OverlayOpacity = 49 }, func(s *AppearanceSettings) { s.OverlayOpacity = 101 },
		func(s *AppearanceSettings) { s.ImageAssetID = stringPointer("../picture.png") }, func(s *AppearanceSettings) { s.BackgroundMode = "local-image" },
	} {
		settings := defaults
		edit(&settings)
		if _, err := validateAppearanceSettings(settings); err == nil {
			t.Fatalf("accepted %#v", settings)
		}
	}
	settings := defaults
	settings.SolidColor = "#def3e5"
	settings.OverlayOpacity = 50
	validated, err := validateAppearanceSettings(settings)
	if err != nil || validated.SolidColor != "#DEF3E5" {
		t.Fatalf("normalized=%#v err=%v", validated, err)
	}
	for _, option := range appearanceOptions().ReadingLayouts {
		if option.Supported != (option.Value == "native") {
			t.Fatal("unsupported shared transcript layouts must remain fail closed")
		}
	}
}

func TestAppearanceImagesRequireRealStaticCompleteContent(t *testing.T) {
	validPNG := appearancePNG(t, 2, 3, 41)
	var jpegBytes bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 3, 2))
	picture.Set(0, 0, color.RGBA{R: 120, A: 255})
	if err := jpeg.Encode(&jpegBytes, picture, nil); err != nil {
		t.Fatal(err)
	}
	// An original single RGB(1,2,3) pixel, encoded as static lossless WebP.
	webpBytes, err := base64.StdEncoding.DecodeString("UklGRiAAAABXRUJQVlA4TBQAAAAvAAAAAAdQgVQIIAAKmv7HiIj+Bw==")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{validPNG, jpegBytes.Bytes(), webpBytes} {
		result, err := validateAppearanceImage(data)
		if err != nil || result.Width < 1 || result.Height < 1 || !appearanceAssetPattern.MatchString(result.AssetID) {
			t.Fatalf("static image format=%s err=%v", result.Format, err)
		}
	}
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk[:4], 8)
	copy(chunk[4:8], "acTL")
	binary.BigEndian.PutUint32(chunk[8:12], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	animatedPNG := append(append(append([]byte{}, validPNG[:33]...), chunk...), validPNG[33:]...)
	for _, data := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), []byte("GIF89a"), validPNG[:len(validPNG)-9], animatedPNG, append(append([]byte{}, validPNG...), 'x'), make([]byte, appearanceMaximumImageBytes+1), appearancePNG(t, 4097, 1, 10)} {
		if _, err := validateAppearanceImage(data); err == nil {
			t.Fatal("accepted spoofed, oversized, animated or truncated image")
		}
	}
	animatedWebp := append([]byte{}, webpBytes...)
	animatedWebp = append(animatedWebp, []byte{'A', 'N', 'I', 'M', 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(animatedWebp[4:8], uint32(len(animatedWebp)-8))
	if _, err := validateAppearanceImage(animatedWebp); err == nil {
		t.Fatal("accepted animated WebP container")
	}
}

func TestAppearanceAssetStoreIsBoundedAndNeverStoresSourcePath(t *testing.T) {
	root := t.TempDir()
	sources := filepath.Join(root, "chosen-files")
	if err := os.MkdirAll(sources, 0o700); err != nil {
		t.Fatal(err)
	}
	store := newAppearanceAssetStore(filepath.Join(root, "State"))
	var first AppearanceImageResult
	for index := 0; index <= appearanceMaximumAssets; index++ {
		path := filepath.Join(sources, "chosen.png")
		if err := os.WriteFile(path, appearancePNG(t, 2, 2, uint8(index+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := store.importImage(path)
		if index == appearanceMaximumAssets {
			if err == nil {
				t.Fatal("capacity must reject another source without eviction")
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = result
		}
		url, err := store.dataURL(result.AssetID)
		if err != nil || !strings.HasPrefix(url, "data:image/png;base64,") || strings.Contains(url, sources) {
			t.Fatal("renderer must receive validated image bytes, not private path")
		}
	}
	entries, err := os.ReadDir(store.directory)
	if err != nil || len(entries) != appearanceMaximumAssets {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	path := filepath.Join(sources, "duplicate.fake-extension")
	if err := os.WriteFile(path, appearancePNG(t, 2, 2, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := store.importImage(path); err != nil || result.AssetID != first.AssetID {
		t.Fatalf("content-addressed duplicate=%#v err=%v", result, err)
	}
	if _, err := store.dataURL("../../chosen-files/chosen.png"); err == nil {
		t.Fatal("path accepted as asset")
	}
	if err := os.WriteFile(filepath.Join(store.directory, first.AssetID), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.dataURL(first.AssetID); err == nil {
		t.Fatal("tampered owned content accepted")
	}
}

func TestAppearanceAssetsRejectSymlinkEscapeAndStorageFailure(t *testing.T) {
	root := t.TempDir()
	store := newAppearanceAssetStore(filepath.Join(root, "State"))
	source := filepath.Join(root, "source.png")
	if err := os.WriteFile(source, appearancePNG(t, 2, 2, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.directory, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.importImage(source); err == nil {
		t.Fatal("write failure was hidden")
	}
	store = newAppearanceAssetStore(filepath.Join(root, "second", "State"))
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	id := SecureFingerprintBytes(appearancePNG(t, 2, 2, 1))
	if err := os.Symlink(source, filepath.Join(store.directory, id)); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := store.dataURL(id); err == nil {
		t.Fatal("asset symlink was followed")
	}
}
