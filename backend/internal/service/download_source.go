package service

import (
	"strings"
	"sync/atomic"
)

// 下载源（更新清单 / 安装包 / 插件市场）用户设置："cdn"（默认，国内边缘加速）
// 或 "github"（直连）。设置只决定候选顺序——首选源改写在前、另一侧原始 URL
// 兜底在后（mirrorFetch 逐个尝试），所以任一侧挂掉都还能下载。
var downloadMirror atomic.Value // string

// SetDownloadMirror 更新生效中的下载源（设置保存与启动加载时调用）。
func SetDownloadMirror(mode string) { downloadMirror.Store(mode) }

// DownloadMirrorMode 返回当前下载源，未设置/未知值一律按 "cdn" 处理。
func DownloadMirrorMode() string {
	if v, _ := downloadMirror.Load().(string); v == "github" {
		return "github"
	}
	return "cdn"
}

// CDN 前缀 ⇄ GitHub 原始地址的双射：市场索引与 .sekplugin 在市场仓库（raw），
// 安装包在应用仓库的 GitHub Releases；两者在项目 OSS 桶都有同路径镜像。
var downloadRoutePairs = [][2]string{
	{"https://sakimizuki.accr.cc/sekaitext-plugins/", "https://raw.githubusercontent.com/SnowGlow-aww/sekaitext-plugins/main/"},
	{"https://sakimizuki.accr.cc/sekaitext-releases/", "https://github.com/SnowGlow-aww/SekaiText-Next/releases/download/"},
}

// routeDownloadURL 按下载源设置返回候选顺序：首选源的改写在前、原始 URL 兜底
// 在后。与两个源都无关的 URL（自建索引、其他 GitHub 仓库…）原样单候选返回。
func routeDownloadURL(rawurl string) []string {
	github := DownloadMirrorMode() == "github"
	for _, pair := range downloadRoutePairs {
		cdn, gh := pair[0], pair[1]
		if github {
			if rest, ok := strings.CutPrefix(rawurl, cdn); ok {
				return []string{gh + rest, rawurl}
			}
			if rest, ok := strings.CutPrefix(rawurl, gh); ok {
				return []string{rawurl, cdn + rest}
			}
		} else {
			if rest, ok := strings.CutPrefix(rawurl, gh); ok {
				return []string{cdn + rest, rawurl}
			}
			if rest, ok := strings.CutPrefix(rawurl, cdn); ok {
				return []string{rawurl, gh + rest}
			}
		}
	}
	return []string{rawurl}
}
