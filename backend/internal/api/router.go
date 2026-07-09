package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/cors"
	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/service"
)

// NewRouter creates and returns a chi router with all routes and middleware configured.
func NewRouter(cfg *config.AppConfig) http.Handler {
	logBuf := service.NewLogBuffer(200)
	h := NewHandler(cfg, logBuf)
	r := chi.NewRouter()

	// Middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(requestLogger(logBuf))

	// CORS - allow all origins in dev.
	// NOTE: '*' is intentionally permissive because the Tauri webview origin
	// (tauri://localhost) varies by platform/dev-server; left unrestricted by design.
	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	r.Use(corsHandler.Handler)
	r.Use(capabilityToken(cfg.AuthToken))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Debug
		r.Get("/debug/logs", h.DebugLogs)
		r.Post("/debug/save", h.DebugSaveLogs)

		// Story navigation
		r.Route("/story", func(r chi.Router) {
			r.Get("/types", h.StoryTypes)
			r.Get("/sorts", h.StorySorts)
			r.Get("/index", h.StoryIndex)
			r.Get("/chapter", h.StoryChapter)
			r.Get("/json-path", h.JsonPath)
			r.Post("/load", h.StoryLoad)
			r.Post("/load-local", h.StoryLoadLocal)
			r.Post("/resolve-label", h.ResolveLabel)
			r.Post("/download-json", h.DownloadStoryJSON)
			r.Get("/download-progress", h.DownloadProgress)
		})

		// Translation file operations
		r.Route("/translation", func(r chi.Router) {
			r.Post("/create", h.TranslationCreate)
			r.Post("/load", h.TranslationLoad)
			r.Post("/load-content", h.TranslationLoadContent)
			r.Post("/save", h.TranslationSave)
			r.Post("/ensure-dir", h.EnsureDir)
			r.Post("/serialize", h.TranslationSerialize)
			r.Post("/check-lines", h.CheckLines)
		})

		// Editor operations
		r.Route("/editor", func(r chi.Router) {
			r.Post("/change-text", h.ChangeText)
			r.Post("/add-line", h.AddLine)
			r.Post("/remove-line", h.RemoveLine)
			r.Post("/compare", h.Compare)
			r.Post("/replace-brackets", h.ReplaceBrackets)
		})

		// Text check
		r.Post("/check/text", h.CheckText)

		// Flashback
		r.Route("/flashback", func(r chi.Router) {
			r.Post("/analyze", h.FlashbackAnalyze)
			r.Get("/clue-hints", h.ClueHints)
			r.Get("/voice-clues", h.VoiceClues)
		})

		// Voice
		r.Get("/voice/url", h.VoiceURL)

		// Live2D asset proxy
		r.Get("/live2d/proxy", h.Live2DProxy)

		// Speaker
		r.Post("/speaker/count", h.SpeakerCount)

		// Settings
		r.Get("/settings", h.GetSettings)
		r.Put("/settings", h.PutSettings)

		// Reveal the app data directory in the OS file manager
		r.Post("/open-data-dir", h.OpenDataDir)

		// Plugins: list/enable/uninstall + static file serving (entry.js, assets)
		r.Route("/plugins", func(r chi.Router) {
			r.Get("/list", h.PluginsList)
			r.Post("/install", h.PluginInstall)
			r.Post("/{id}/enabled", h.PluginSetEnabled)
			r.Delete("/{id}", h.PluginUninstall)
			r.Get("/{id}/files/*", h.PluginFile)
		})

		// Plugin marketplace: remote index browse + install-by-id + auto-update
		r.Route("/market", func(r chi.Router) {
			r.Get("/index", h.MarketIndex)
			r.Post("/install", h.MarketInstall)
			r.Post("/auto-update", h.MarketAutoUpdate)
		})

		// App self-update: check the release manifest, download the installer
		// (mirror-accelerated) to Downloads, then open it for the user to install.
		r.Route("/app", func(r chi.Router) {
			r.Get("/update/check", h.AppUpdateCheck)
			r.Post("/update/download", h.AppUpdateDownload)
			r.Get("/update/download-progress", h.DownloadProgress)
			r.Post("/open", h.AppUpdateOpen)
		})

		// Import a folder of Live2D assets into the local mirror
		r.Post("/live2d/import", h.ImportLive2D)

		// Download/update the online Live2D asset library: diff the upstream
		// model_list against the local mirror and fetch the missing models +
		// their motion data. Start returns {taskId}; poll sync-progress.
		r.Post("/live2d/sync", h.Live2DSync)
		r.Get("/live2d/sync-progress", h.Live2DSyncProgress)

		// Glossary (term library): search, browse, CRUD, import, sync, appellations
		r.Route("/glossary", func(r chi.Router) {
			r.Get("/search", h.GlossarySearch)
			r.Get("/categories", h.GlossaryCategories)
			r.Get("/entries", h.GlossaryEntries)
			r.Post("/entries", h.GlossaryAddEntry)
			r.Put("/entries/{id}", h.GlossaryUpdateEntry)
			r.Delete("/entries/{id}", h.GlossaryDeleteEntry)
			r.Post("/import", h.GlossaryImport)
			r.Post("/reload", h.GlossaryReload)
			r.Post("/sync", h.GlossarySync)
			r.Get("/export", h.GlossaryExport)
			r.Get("/grammar", h.GlossaryGrammar)
			// Appellation lookup (人称表)
			r.Get("/appellations", h.GlossaryAppellationLookup)
			r.Put("/appellations", h.GlossaryAppellationUpsert)
			r.Get("/appellations/speakers", h.GlossaryAppellationSpeakers)
			r.Get("/appellations/targets", h.GlossaryAppellationTargets)
		})

		// Team mode: proxy to a remote glossary-server (login, sync, proposals,
		// review, admin). The local backend holds the token + skips the server's
		// self-signed TLS; the frontend only ever talks to localhost.
		r.Route("/team", func(r chi.Router) {
			r.Get("/status", h.TeamStatus)
			r.Post("/login", h.TeamLogin)
			r.Post("/logout", h.TeamLogout)
			r.Post("/connect", h.TeamConnect)
			r.Post("/disconnect", h.TeamDisconnect)
			r.Post("/sync", h.TeamSync)
			r.Post("/proposals", h.TeamCreateProposal)
			r.Get("/proposals/mine", h.TeamMyProposals)
			r.Delete("/proposals/{id}", h.TeamWithdrawProposal)
			r.Get("/proposals", h.TeamPendingProposals)
			r.Post("/proposals/{id}/approve", h.TeamApproveProposal)
			r.Post("/proposals/{id}/reject", h.TeamRejectProposal)
			r.Post("/admin/users", h.TeamCreateUser)
			r.Post("/admin/reviewers", h.TeamSetReviewer)
			r.Get("/admin/users", h.TeamListUsers)
			r.Post("/admin/users/{id}/role", h.TeamSetUserRole)
			r.Post("/admin/users/{id}/status", h.TeamSetUserStatus)
			r.Post("/admin/users/{id}/reset-password", h.TeamResetUserPassword)
			r.Delete("/admin/users/{id}", h.TeamDeleteUser)
			r.Post("/admin/glossary/bulk-import", h.TeamBulkImport)
			// account self-service
			r.Post("/account/password", h.TeamChangePassword)
			r.Post("/account/profile", h.TeamUpdateProfile)
			r.Get("/account/users", h.TeamUserList)
		})

		// Recovery (autosave)
		r.Route("/recovery", func(r chi.Router) {
			r.Post("/save", h.RecoverySave)
			r.Get("/load", h.RecoveryLoad)
			r.Delete("/clear", h.RecoveryClear)
			r.Post("/clear", h.RecoveryClear) // for sendBeacon on beforeunload
		})

		// Update (CDN refresh)
		r.Post("/update", h.Update)
		r.Get("/update/progress", h.UpdateProgress)

		// Auto-timing (打轴) + suppress (压制) via the SekaiCoreEngine sidecar
		r.Route("/engine", func(r chi.Router) {
			r.Get("/status", h.EngineStatus)
			r.Post("/timing/start", h.EngineTimingStart)
			r.Get("/timing/progress", h.EngineTimingProgress)
			r.Get("/timing/preview", h.EngineTimingPreview)
			r.Post("/timing/export", h.EngineTimingExport)
			r.Get("/timing/lines", h.EngineTimingLines)
			r.Post("/timing/line/separator", h.EngineTimingLineSeparator)
			r.Post("/timing/line/translation", h.EngineTimingLineTranslation)
			r.Post("/timing/line/estimate", h.EngineTimingLineEstimate)
			r.Get("/timing/frame", h.EngineTimingFrame)
			r.Get("/timing/sync/status", h.EngineTimingSyncStatus)
			r.Post("/timing/sync/push", h.EngineTimingSyncPush)
			r.Post("/timing/sync/pull", h.EngineTimingSyncPull)
			r.Post("/suppress/start", h.EngineSuppressStart)
			r.Get("/suppress/progress", h.EngineSuppressProgress)
			r.Post("/cancel", h.EngineCancel)
		})

		// Assets
		r.Route("/assets", func(r chi.Router) {
			r.Get("/characters", h.Characters)
			r.Get("/character-icon/{index}", h.CharacterIcon)
			r.Get("/character-icon-custom", h.CharacterIconCustomStatus)
			r.Post("/character-icon-custom", h.CharacterIconCustomImport)
			r.Delete("/character-icon-custom", h.CharacterIconCustomReset)
			r.Get("/units", h.Units)
			r.Get("/areas", h.Areas)
		})
	})

	return r
}

// capabilityToken rejects mutating requests that don't carry the per-launch
// X-Sekai-Token (set by the Tauri shell, forwarded by the frontend fetch wrapper
// in main.ts). Enforced only when token != "" (production). GET/HEAD/OPTIONS and a
// few routes that can't carry the header are exempt: recovery/clear (sendBeacon),
// and the plugin-driven engine/live2d routes — none of which are part of the
// settings → download → open RCE surface this guards.
func capabilityToken(token string) func(http.Handler) http.Handler {
	exempt := map[string]bool{
		"/api/v1/recovery/clear":       true, // sendBeacon on beforeunload (headers impossible)
		"/api/v1/live2d/import":        true, // invoked by the Live2D plugin bundle
		"/api/v1/live2d/sync":          true, // invoked by the Live2D plugin bundle
		"/api/v1/live2d/sync-progress": true, // GET; listed for parity (GET is never token-checked)
	}
	mutating := func(m string) bool {
		switch m {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			return true
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token != "" && mutating(r.Method) &&
				!exempt[r.URL.Path] && !strings.HasPrefix(r.URL.Path, "/api/v1/engine/") {
				got := r.Header.Get("X-Sekai-Token")
				if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"error":"missing or invalid capability token"}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger is a custom middleware that logs requests and writes to the log buffer.
func requestLogger(buf *service.LogBuffer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			latency := time.Since(start)
			msg := fmt.Sprintf("%s %s %d %s",
				r.Method, r.URL.Path, ww.Status(), latency.Round(time.Millisecond))
			buf.Write(msg)
			fmt.Println("[server]", msg)
		})
	}
}
