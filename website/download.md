---
title: 下载
---

# 下载 SekaiText Next

选择与你的设备对应的版本。安装包由本站 CDN 直接提供；应用内发现新版本后，也会通过同一线路完成更新。

<DownloadButtons style="margin: 32px 0 40px" />

## 系统要求

| 平台 | 要求 | 安装包 |
|------|------|--------|
| macOS | Apple 芯片（M 系列），macOS 12 及以上 | `.dmg` |
| Windows | x64，Windows 10 及以上 | `.exe` 安装程序 |

应用内更新会在启动安装程序前核对发布清单中的文件大小和 SHA-256；任一项不匹配都会拒绝启动该安装包。

## 安装提示

### macOS

打开 `.dmg`，把 **SekaiText Next** 拖入「应用程序」文件夹。安装包暂未进行 Apple 开发者签名，因此首次启动时可能出现系统拦截：

1. 在「应用程序」中 **右键 → 打开**，再点「打开」；
2. 如提示「已损坏，无法打开」，在终端执行后重新打开：

```bash
sudo xattr -cr "/Applications/SekaiText Next.app"
```

::: tip
这是 macOS 对未签名应用的安全提示，不代表安装包在下载过程中损坏。
:::

### Windows

首次运行如出现 SmartScreen 蓝色提示框，点击 **「更多信息」→「仍要运行」**。

## 更新

应用会自动检查更新。发现新版本后，可直接在提示中下载安装；更新包同样走国内 CDN。

需要回看更新内容或下载历史版本时，请前往 [GitHub Releases](https://github.com/SnowGlow-aww/SekaiText-Next/releases)。
