package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"
)

const (
	appearanceMaximumImageBytes     = 5 << 20
	appearanceMaximumImageDimension = 4096
	appearanceMaximumAssets         = 8
	appearanceMaximumAssetBytes     = 40 << 20
)

// The owned directory has a hard capacity. A full store refuses another import;
// it neither evicts saved assets nor scans the user's source directories.
type appearanceAssetStore struct {
	mu            sync.Mutex
	directory     string
	cachedID      string
	cachedDataURL string
}

func newAppearanceAssetStore(stateDirectory string) *appearanceAssetStore {
	return &appearanceAssetStore{directory: filepath.Join(filepath.Dir(stateDirectory), "AppearanceAssets")}
}

func readAppearanceImage(path string) ([]byte, AppearanceImageResult, error) {
	if !filepath.IsAbs(path) || strings.Contains(path, "://") {
		return nil, AppearanceImageResult{}, appearanceError("imageRead")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, AppearanceImageResult{}, appearanceError("imageRead")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, AppearanceImageResult{}, appearanceError("imageRead")
	}
	if info.Size() > appearanceMaximumImageBytes {
		return nil, AppearanceImageResult{}, appearanceError("imageSize")
	}
	data, err := io.ReadAll(io.LimitReader(file, appearanceMaximumImageBytes+1))
	if err != nil {
		return nil, AppearanceImageResult{}, appearanceError("imageRead")
	}
	if len(data) > appearanceMaximumImageBytes {
		return nil, AppearanceImageResult{}, appearanceError("imageSize")
	}
	result, err := validateAppearanceImage(data)
	return data, result, err
}

func validateAppearanceImage(data []byte) (AppearanceImageResult, error) {
	if len(data) == 0 || len(data) > appearanceMaximumImageBytes {
		return AppearanceImageResult{}, appearanceError("imageSize")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !containsString([]string{"png", "jpeg", "webp"}, format) {
		return AppearanceImageResult{}, appearanceError("imageType")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > appearanceMaximumImageDimension || config.Height > appearanceMaximumImageDimension {
		return AppearanceImageResult{}, appearanceError("imageDimensions")
	}
	if !appearanceStaticImageContainer(data, format) {
		return AppearanceImageResult{}, appearanceError("imageType")
	}
	// DecodeConfig alone accepts truncated headers. Decode the very same bounded
	// byte slice before it becomes a renderer source or a persisted asset.
	decoded, actualFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || actualFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return AppearanceImageResult{}, appearanceError("imageType")
	}
	return AppearanceImageResult{AssetID: SecureFingerprintBytes(data), Width: config.Width, Height: config.Height, Format: format}, nil
}

func appearanceStaticImageContainer(data []byte, format string) bool {
	switch format {
	case "png":
		if len(data) < 8 {
			return false
		}
		for offset := 8; offset+12 <= len(data); {
			length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
			end := uint64(offset) + length + 12
			if end > uint64(len(data)) {
				return false
			}
			kind := string(data[offset+4 : offset+8])
			if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
				return false
			}
			if kind == "IEND" {
				return end == uint64(len(data))
			}
			offset = int(end)
		}
		return false
	case "webp":
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
			return false
		}
		for offset := 12; offset+8 <= len(data); {
			kind := string(data[offset : offset+4])
			length := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			end := uint64(offset) + 8 + length + (length & 1)
			if end > uint64(len(data)) || kind == "ANIM" || kind == "ANMF" {
				return false
			}
			if kind == "VP8X" && length > 0 && data[offset+8]&2 != 0 {
				return false
			}
			offset = int(end)
			if offset == len(data) {
				return true
			}
		}
		return false
	case "jpeg":
		return len(data) >= 4 && data[0] == 0xff && data[1] == 0xd8 && data[len(data)-2] == 0xff && data[len(data)-1] == 0xd9
	}
	return false
}

func (s *appearanceAssetStore) resolvedDirectory() (string, error) {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return "", appearanceError("imageWrite")
	}
	info, err := os.Lstat(s.directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", appearanceError("assetUnavailable")
	}
	resolved, err := filepath.EvalSymlinks(s.directory)
	if err != nil {
		return "", appearanceError("assetUnavailable")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(s.directory))
	if err != nil || !strings.EqualFold(filepath.Dir(resolved), parent) {
		return "", appearanceError("assetUnavailable")
	}
	return resolved, nil
}

func (s *appearanceAssetStore) importImage(path string) (AppearanceImageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, result, err := readAppearanceImage(path)
	if err != nil {
		return AppearanceImageResult{}, err
	}
	directory, err := s.resolvedDirectory()
	if err != nil {
		return AppearanceImageResult{}, err
	}
	destination := filepath.Join(directory, result.AssetID)
	if existing, err := s.readAssetLocked(result.AssetID, directory); err == nil {
		if SecureFingerprintBytes(existing) == result.AssetID {
			return result, nil
		}
	} else if _, statErr := os.Lstat(destination); statErr == nil || !os.IsNotExist(statErr) {
		return AppearanceImageResult{}, appearanceError("imageWrite")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return AppearanceImageResult{}, appearanceError("imageWrite")
	}
	var count int
	var total int64
	for _, entry := range entries {
		// Unexpected nodes cannot be taken as space to overwrite or remove.
		if !appearanceAssetPattern.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			return AppearanceImageResult{}, appearanceError("imageWrite")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return AppearanceImageResult{}, appearanceError("imageWrite")
		}
		count++
		total += info.Size()
	}
	if count >= appearanceMaximumAssets || total+int64(len(data)) > appearanceMaximumAssetBytes {
		return AppearanceImageResult{}, appearanceError("imageWrite")
	}
	if err := writeFileAtomic(destination, data, 0o600); err != nil {
		return AppearanceImageResult{}, appearanceError("imageWrite")
	}
	return result, nil
}

func (s *appearanceAssetStore) readAssetLocked(id, directory string) ([]byte, error) {
	if !appearanceAssetPattern.MatchString(id) {
		return nil, appearanceError("assetUnavailable")
	}
	path := filepath.Join(directory, id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, appearanceError("assetUnavailable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Dir(resolved), directory) {
		return nil, appearanceError("assetUnavailable")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, appearanceError("assetUnavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() > appearanceMaximumImageBytes {
		return nil, appearanceError("assetUnavailable")
	}
	data, err := io.ReadAll(io.LimitReader(file, appearanceMaximumImageBytes+1))
	if err != nil || len(data) > appearanceMaximumImageBytes || SecureFingerprintBytes(data) != id {
		return nil, appearanceError("assetUnavailable")
	}
	return data, nil
}

func (s *appearanceAssetStore) dataURL(id string) (string, error) {
	return s.dataURLContext(context.Background(), id)
}

func (s *appearanceAssetStore) dataURLContext(ctx context.Context, id string) (string, error) {
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return "", err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := s.resolvedDirectory()
	if err != nil {
		return "", err
	}
	data, err := s.readAssetLocked(id, directory)
	if err != nil {
		return "", err
	}
	// At most one encoded source is cached. The current file is still checked
	// against its content-addressed ID before reusing already validated bytes.
	if s.cachedID == id {
		return s.cachedDataURL, nil
	}
	result, err := validateAppearanceImage(data)
	if err != nil {
		return "", appearanceError("assetUnavailable")
	}
	s.cachedID = id
	s.cachedDataURL = "data:image/" + result.Format + ";base64," + base64.StdEncoding.EncodeToString(data)
	return s.cachedDataURL, nil
}
