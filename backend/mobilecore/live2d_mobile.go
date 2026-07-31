package mobilecore

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"sekaitext/backend/internal/service"
)

var mobileLive2DState struct {
	mu    sync.RWMutex
	store *service.Live2DAssetStore
}

type live2DAssetRequest struct {
	URL string `json:"url"`
}

// InitializeLive2DAssetCache configures the app-private on-demand Live2D cache.
func InitializeLive2DAssetCache(cacheDir string) error {
	store, err := service.NewLive2DAssetStore(cacheDir)
	if err != nil {
		return fmt.Errorf("initialize mobile live2d cache: %w", err)
	}
	mobileLive2DState.mu.Lock()
	mobileLive2DState.store = store
	mobileLive2DState.mu.Unlock()
	return nil
}

func live2DAssetStore() (*service.Live2DAssetStore, error) {
	mobileLive2DState.mu.RLock()
	store := mobileLive2DState.store
	mobileLive2DState.mu.RUnlock()
	if store == nil {
		return nil, fmt.Errorf("mobile live2d cache is not initialized")
	}
	return store, nil
}

// ResolveLive2DAsset validates, downloads, and returns one cached resource path.
func ResolveLive2DAsset(requestJSON string) (string, error) {
	store, err := live2DAssetStore()
	if err != nil {
		return "", err
	}
	var req live2DAssetRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("decode live2d asset request: %w", err)
	}
	if strings.TrimSpace(req.URL) == "" {
		return "", fmt.Errorf("resolve live2d asset: URL is required")
	}
	result, err := store.Resolve(req.URL)
	if err != nil {
		return "", fmt.Errorf("resolve live2d asset: %w", err)
	}
	return encode(result)
}

func mobileLive2DReady() bool {
	mobileLive2DState.mu.RLock()
	ready := mobileLive2DState.store != nil
	mobileLive2DState.mu.RUnlock()
	return ready
}
