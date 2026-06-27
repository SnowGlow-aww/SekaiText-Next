package config

import (
	"path/filepath"
	"runtime"
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
	// PluginsDir is the writable directory holding installed plugins, one subdir
	// per plugin id ({PluginsDir}/<id>/{manifest.json,entry.js,...}). Served by the
	// Go sidecar so plugins can be installed/uninstalled at runtime (the bundled
	// frontend dist is read-only in the packaged app). NOTE: no first-party
	// seeding from the read-only bundle is performed on startup.
	PluginsDir string
	// EnginePath / FfmpegPath point at the bundled SekaiToolsEngine sidecar and the
	// libass-enabled ffmpeg used for 压制. They live under {baseDir}/engine/ so the
	// .NET apphost sits beside its native dylibs (Tauri bundle.resources maps the
	// whole publish folder there). Empty/missing => the 打轴/压制 feature is disabled.
	EnginePath string
	FfmpegPath string
}

// NewAppConfig creates an AppConfig.
// baseDir is for read-only bundled resources.
// dataDir is for writable app data (defaults to baseDir if empty).
func NewAppConfig(baseDir, dataDir string) *AppConfig {
	if dataDir == "" {
		dataDir = baseDir
	}
	engineName, ffmpegName := "SekaiToolsEngine", "ffmpeg"
	if runtime.GOOS == "windows" {
		engineName, ffmpegName = "SekaiToolsEngine.exe", "ffmpeg.exe"
	}
	return &AppConfig{
		BaseDir:        baseDir,
		DataBaseDir:    dataDir,
		CatalogDir:     filepath.Join(dataDir, "resources", "catalog"),
		DataDir:        filepath.Join(dataDir, "resources", "data"),
		ImagesDir:      filepath.Join(baseDir, "resources", "images"),
		ImagesChrDir:   filepath.Join(baseDir, "resources", "images", "chr"),
		Live2DLocalDir: filepath.Join(dataDir, "resources", "live2d"),
		PluginsDir:     filepath.Join(dataDir, "plugins"),
		EnginePath:     filepath.Join(baseDir, "engine", engineName),
		FfmpegPath:     filepath.Join(baseDir, "engine", ffmpegName),
	}
}

// DefaultBaseDir returns "."
func DefaultBaseDir() string {
	return "."
}
