package config

import (
	"path/filepath"
)

// AppConfig holds all path configurations.
// Resource paths (ImagesDir, ImagesChrDir) are relative to BaseDir (bundled assets, read-only).
// Data paths (CatalogDir, DataDir) are relative to DataBaseDir (writable app data).
type AppConfig struct {
	BaseDir      string
	DataBaseDir  string
	CatalogDir   string
	DataDir      string
	ImagesDir    string
	ImagesChrDir string
	// Live2DLocalDir is an optional local mirror of the Live2D asset library
	// (model/ + motion/ + model_list.json). When a requested asset exists here,
	// the proxy serves it from disk instead of hitting the CDN. Lives alongside
	// the downloaded story JSON under the app data dir: {dataDir}/resources/live2d.
	Live2DLocalDir string
}

// NewAppConfig creates an AppConfig.
// baseDir is for read-only bundled resources.
// dataDir is for writable app data (defaults to baseDir if empty).
func NewAppConfig(baseDir, dataDir string) *AppConfig {
	if dataDir == "" {
		dataDir = baseDir
	}
	return &AppConfig{
		BaseDir:        baseDir,
		DataBaseDir:    dataDir,
		CatalogDir:     filepath.Join(dataDir, "resources", "catalog"),
		DataDir:        filepath.Join(dataDir, "resources", "data"),
		ImagesDir:      filepath.Join(baseDir, "resources", "images"),
		ImagesChrDir:   filepath.Join(baseDir, "resources", "images", "chr"),
		Live2DLocalDir: filepath.Join(dataDir, "resources", "live2d"),
	}
}

// DefaultBaseDir returns "."
func DefaultBaseDir() string {
	return "."
}
