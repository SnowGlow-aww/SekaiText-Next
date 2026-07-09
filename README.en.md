<!-- markdownlint-disable-next-line MD036 -->
_✨ Project Sekai Story Translation Editor ✨_

<!-- prettier-ignore-start -->
<!-- markdownlint-disable-next-line MD036 -->

<p align="center">
  <a href="https://github.com/SnowGlow-aww/SekaiText-Next/blob/main/README.md">简体中文</a> |
  <a href="https://github.com/SnowGlow-aww/SekaiText-Next/blob/main/README.en.md">English</a>
</p>

<p align="center">
  <img alt="SekaiText" src="public/app-icon.png" width="128" height="128" />
</p>

<p align="center">
  <img alt="License" src="https://img.shields.io/github/license/SnowGlow-aww/SekaiText-Next?style=flat-square&color=ff69b4" />
  <img alt="Version" src="https://img.shields.io/badge/version-5.3.0-blue?style=flat-square" />
  <img alt="Tauri" src="https://img.shields.io/badge/Tauri-v2-ffc131?style=flat-square&logo=tauri&logoColor=white" />
  <img alt="Vue" src="https://img.shields.io/badge/Vue-3.5-4fc08d?style=flat-square&logo=vuedotjs&logoColor=white" />
  <img alt="Go" src="https://img.shields.io/badge/Go-1.24-00add8?style=flat-square&logo=go&logoColor=white" />
</p>

<p align="center">
  <img alt="Platforms" src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-6c5ce7?style=flat-square" />
  <a href="https://sakimizuki.accr.cc/web/index.html"><img alt="Website" src="https://img.shields.io/badge/website-sakimizuki.accr.cc-39c5bb?style=flat-square" /></a>
</p>

<!-- prettier-ignore-end -->

SekaiText is a desktop application for browsing, translating, and proofreading Project Sekai: Colorful Stage! story scenarios. It combines a Vue 3 frontend with a Go backend, packaged as a Tauri desktop app, with a built-in auto-timing / encoding engine, a Live2D story player, and a hot-loadable plugin system.

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Frontend | [Vue 3](https://vuejs.org/) + [TypeScript](https://www.typescriptlang.org/) + [Vite](https://vitejs.dev/) + [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI](https://daisyui.com/) |
| State | [Pinia](https://pinia.vuejs.org/) |
| Routing | [Vue Router v5](https://router.vuejs.org/) |
| Backend | [Go 1.24](https://go.dev/) + [chi router](https://github.com/go-chi/chi) (TCP in dev; stdio + `sekai://` custom scheme in release, no port) |
| Desktop Shell | [Tauri v2](https://v2.tauri.app/) (Rust) |
| Timing / Encoding Engine | SekaiCoreEngine (C# / .NET) + [FFmpeg](https://ffmpeg.org/), driven by the backend over NDJSON IPC as a sidecar |
| Plugins | Hot-loadable plugins sharing the host Vue / Pinia / Router singletons |
| Icons | [Lucide](https://lucide.dev/) via `lucide-vue-next` |
| Tables | [TanStack Table](https://tanstack.com/table/v8) + [TanStack Virtual](https://tanstack.com/virtual/v3) |

## Project Structure

```
sekaitext/
├── backend/                 # Go backend server
│   ├── cmd/sekaitext/main.go # Entry point (TCP:9800 in dev; stdio + sekai:// in release)
│   └── internal/
│       ├── api/             # Handlers + router (chi)
│       ├── config/          # App configuration
│       ├── ipc/             # stdio framing + engine NDJSON IPC
│       ├── model/           # Data types
│       └── service/         # Business logic (timing / glossary / Live2D / plugins / self-update)
├── src/                     # Vue 3 frontend
│   ├── api/                 # API client
│   ├── components/          # Vue components
│   ├── composables/         # Shared composables
│   ├── pages/               # Route pages
│   ├── stores/              # Pinia stores
│   └── types/               # TypeScript types
├── src-tauri/               # Tauri desktop shell (Rust)
│   ├── capabilities/        # Tauri v2 permission capabilities
│   └── src/                 # Rust source
├── scripts/                 # Utility scripts
├── public/                  # Static assets
└── package.json
```

## Getting Started

### Prerequisites

- [Node.js](https://nodejs.org/) >= 20
- [Go](https://go.dev/) >= 1.24
- [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/) (Rust toolchain, system dependencies)

### Install

```bash
# Install frontend dependencies
npm install

# Install Tauri CLI
npm install -D @tauri-apps/cli
```

### Development

```bash
# Start both Go backend and Vite dev server concurrently
npm run dev:web

# Or start with Tauri desktop window
npm run dev:tauri
```

In dev the Go API server binds `http://localhost:9800` and the Vite dev server runs on `http://localhost:5173`; the release build uses the stdio + `sekai://` custom scheme instead and binds no port.

### Build

```bash
# Build the Tauri desktop application
npm run build:tauri
```

## Configuration

Settings are available in-app via the Settings page:

- **Font Size** — Editor text display size (10–48px)
- **Index Order** — Story index dropdown sort order
- **Save \\N** — Preserve `\N` line break markers in translation files
- **Save Voice** — Download and save voice files locally
- **SSL Verification** — Disable SSL certificate verification (needed in some network environments)
- **Dark Mode** — Toggle between light and dark themes
- **Debug Log** — Show debug log panel at the bottom of the editor

## Version

Current version: **5.3.0** (stable)

## License

This project is licensed under the [MIT License](LICENSE).

*Project Sekai is a trademark of SEGA / Colorful Palette. This project is for educational and fan-translation purposes only.*
