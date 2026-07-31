package mobilecore

import (
	"strings"
	"testing"
)

func TestInitializeAndRejectUnsafeLive2DAsset(t *testing.T) {
	if err := InitializeLive2DAssetCache(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !mobileLive2DReady() {
		t.Fatal("live2d cache should be ready after initialization")
	}
	if _, err := ResolveLive2DAsset(`{"url":""}`); err == nil || !strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("unexpected empty URL error: %v", err)
	}
	if _, err := ResolveLive2DAsset(`{"url":"http://localhost/model.moc3"}`); err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
		t.Fatalf("unsafe URL was not rejected: %v", err)
	}
}
