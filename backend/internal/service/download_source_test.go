package service

import (
	"reflect"
	"testing"
)

func TestRouteDownloadURL(t *testing.T) {
	cdnPkg := "https://sakimizuki.accr.cc/sekaitext-plugins/plugins/live2d-1.1.3.sekplugin"
	ghPkg := "https://raw.githubusercontent.com/SnowGlow-aww/sekaitext-plugins/main/plugins/live2d-1.1.3.sekplugin"
	cdnDmg := "https://sakimizuki.accr.cc/sekaitext-releases/v5.2.0/SekaiText.Next_5.2.0_aarch64.dmg"
	ghDmg := "https://github.com/SnowGlow-aww/SekaiText-Next/releases/download/v5.2.0/SekaiText.Next_5.2.0_aarch64.dmg"
	cdnManifest := "https://sakimizuki.accr.cc/sekaitext-plugins/app-release.json"
	ghManifest := "https://raw.githubusercontent.com/SnowGlow-aww/sekaitext-plugins/main/app-release.json"
	other := "https://example.com/custom-index.json"

	cases := []struct {
		mode string
		in   string
		want []string
	}{
		// 默认（含未设置）= CDN 加速：GitHub 原始地址改写到 CDN，CDN 地址原样。
		{"", ghPkg, []string{cdnPkg, ghPkg}},
		{"cdn", ghPkg, []string{cdnPkg, ghPkg}},
		{"cdn", ghDmg, []string{cdnDmg, ghDmg}},
		{"cdn", cdnPkg, []string{cdnPkg, ghPkg}},
		{"cdn", cdnDmg, []string{cdnDmg, ghDmg}},
		{"cdn", cdnManifest, []string{cdnManifest, ghManifest}},
		// GitHub 直连：CDN 地址改写回 GitHub，GitHub 地址原样。
		{"github", cdnPkg, []string{ghPkg, cdnPkg}},
		{"github", cdnDmg, []string{ghDmg, cdnDmg}},
		{"github", ghPkg, []string{ghPkg, cdnPkg}},
		{"github", ghDmg, []string{ghDmg, cdnDmg}},
		{"github", ghManifest, []string{ghManifest, cdnManifest}},
		// 与两个源无关的 URL 不动。
		{"cdn", other, []string{other}},
		{"github", other, []string{other}},
	}
	for _, c := range cases {
		SetDownloadMirror(c.mode)
		if got := routeDownloadURL(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("mode=%q url=%s\n got %v\nwant %v", c.mode, c.in, got, c.want)
		}
	}
	SetDownloadMirror("")
}

func TestMirrorCandidatesUsesRoute(t *testing.T) {
	SetDownloadMirror("cdn")
	defer SetDownloadMirror("")
	got := mirrorCandidates("https://raw.githubusercontent.com/SnowGlow-aww/sekaitext-plugins/main/index.json")
	want := []string{
		"https://sakimizuki.accr.cc/sekaitext-plugins/index.json",
		"https://raw.githubusercontent.com/SnowGlow-aww/sekaitext-plugins/main/index.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
