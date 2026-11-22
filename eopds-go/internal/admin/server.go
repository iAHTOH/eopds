// Package admin — HTTP-сервер административной панели и мастера установки.
//
// Стек — чистый stdlib: net/http + html/template + ванильный CSS. CGO выключен.
//
// Логика маршрутов:
//   - если БД не сконфигурирована/недоступна или не установлена → мастер
//     установки (/install);
//   - после установки — главная админки (заглушка на этом этапе).
package admin

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/i18n"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
	"github.com/iAHTOH/eopds/eopds-go/internal/version"
)

// Server — HTTP-сервер админки/установщика.
type Server struct {
	cfg   config.Config
	log   *slog.Logger
	mu    sync.RWMutex
	store store.Store // nil, пока не установлено/не подключено

	// Наборы шаблонов: каждая страница = layout + страница (изолированно).
	pages map[string]*template.Template
	i18n  *i18n.Bundle

	scan *scanState // состояние единственного фонового сканирования
}

// New создаёт сервер и строит наборы шаблонов.
func New(cfg config.Config, log *slog.Logger) (*Server, error) {
	bundle, err := i18n.Load()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: log, pages: make(map[string]*template.Template), i18n: bundle, scan: newScanState()}
	pageFiles := map[string]string{
		"install":         "templates/install.html",
		"home":            "templates/home.html",
		"library":         "templates/library.html",
		"library_catalog": "templates/library_catalog.html",
		"library_books":   "templates/library_books.html",
		"library_authors": "templates/library_authors.html",
		"library_series":  "templates/library_series.html",
		"library_genres":  "templates/library_genres.html",
		"library_book":    "templates/library_book.html",
		"reader":          "templates/reader.html",
		"scan":            "templates/scan.html",
		"settings":        "templates/settings.html",
		"login":           "templates/login.html",
		"portal":          "templates/portal.html",
		"library_home":    "templates/library_home.html",
	}
	for name, file := range pageFiles {
		base := template.New("layout").Funcs(template.FuncMap{
			"T":        func(k string) string { return k },
			"langName": langName,
			"langCode": langCode,
			"roleName": roleName,
			"kb":       kb,
			"trim":     trimText,
		})
		if _, err := base.ParseFS(templatesFS, "templates/layout.html", "templates/chrome.html", "templates/lib_parts.html", "templates/library_entities.html", file); err != nil {
			return nil, err
		}
		s.pages[name] = base
	}
	return s, nil
}

// SetStore устанавливает активное хранилище (после подключения/установки).
func (s *Server) SetStore(st store.Store) {
	s.mu.Lock()
	s.store = st
	s.mu.Unlock()
}

// getStore возвращает текущее хранилище (или nil).
func (s *Server) getStore() store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}

// Handler возвращает корневой http.Handler с маршрутами.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePortal)
	mux.HandleFunc("GET /install", s.handleInstallForm)
	mux.HandleFunc("POST /install", s.handleInstallSubmit)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /dashboard", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("GET /library", s.requireAuth(s.handleLibraryIndex))
	mux.HandleFunc("GET /library/books", s.requireAuth(s.handleLibraryBooks))
	mux.HandleFunc("GET /library/catalog/{id}", s.requireAuth(s.handleLibraryCatalog))
	mux.HandleFunc("GET /library/authors", s.requireAuth(s.handleLibraryAuthors))
	mux.HandleFunc("GET /library/series", s.requireAuth(s.handleLibrarySeries))
	mux.HandleFunc("GET /library/genres", s.requireAuth(s.handleLibraryGenres))
	mux.HandleFunc("GET /library/book/{id}", s.requireAuth(s.handleLibraryBook))
	mux.HandleFunc("GET /library/book/{id}/cover", s.requireAuth(s.handleBookCover))
	mux.HandleFunc("GET /library/book/{id}/download", s.requireAuth(s.handleBookDownload))
	mux.HandleFunc("GET /library/book/{id}/read", s.requireAuth(s.handleBookRead))
	mux.HandleFunc("GET /library/book/{id}/html", s.requireAuth(s.handleBookHTML))
	mux.HandleFunc("POST /library/book/{id}/progress", s.requireAuth(s.handleBookProgress))
	mux.HandleFunc("GET /library/search", s.requireAuth(s.handleLibrarySearch))
	mux.HandleFunc("GET /library/suggest", s.requireAuth(s.handleLibrarySuggest))
	mux.HandleFunc("GET /scan", s.requireAuth(s.handleSection("scan")))
	mux.HandleFunc("POST /scan/run", s.requireAuth(s.requireRole("admin", s.handleScanRun)))
	mux.HandleFunc("GET /scan/status", s.requireAuth(s.handleScanStatus))
	mux.HandleFunc("GET /settings", s.requireAuth(s.requireRole("admin", s.handleSection("settings"))))
	mux.HandleFunc("POST /settings/scan-dirs", s.requireAuth(s.requireRole("admin", s.handleSaveScanPaths)))
	mux.HandleFunc("POST /settings/clear-library", s.requireAuth(s.requireRole("admin", s.handleClearLibrary)))
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	return s.withThemeCookie(mux)
}

// withThemeCookie запоминает выбранную тему в cookie.
func (s *Server) withThemeCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("theme"); isKnownTheme(t) {
			http.SetCookie(w, &http.Cookie{
				Name: "theme", Value: t, Path: "/", MaxAge: 31536000,
				SameSite: http.SameSiteLaxMode,
			})
		}
		if l := r.URL.Query().Get("lang"); s.i18n.Has(l) {
			http.SetCookie(w, &http.Cookie{
				Name: "lang", Value: l, Path: "/", MaxAge: 31536000,
				SameSite: http.SameSiteLaxMode,
			})
		}
		// Числовой языковой фильтр (?lang=0|1|2|3|9) запоминаем отдельно,
		// чтобы он сохранялся при переходах по ссылкам без ?lang=.
		if lv := r.URL.Query().Get("lang"); lv != "" {
			if n, err := strconv.Atoi(lv); err == nil {
				switch n {
				case 0, 1, 2, 3, 9:
					http.SetCookie(w, &http.Cookie{
						Name: "lang_num", Value: lv, Path: "/", MaxAge: 31536000,
						SameSite: http.SameSiteLaxMode,
					})
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isInstalled проверяет, пройден ли мастер установки.
func (s *Server) isInstalled(ctx context.Context) bool {
	st := s.getStore()
	if st == nil {
		return false
	}
	ok, err := st.IsInstalled(ctx)
	if err != nil {
		s.log.Warn("проверка установки", "err", err)
		return false
	}
	return ok
}

// render рендерит страницу (layout + контент) в ответ.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.log.Error("render: unknown page", "page", page)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	tr := s.i18n.Translator(resolveLang(r))
	cloned, err := tmpl.Clone()
	if err != nil {
		s.log.Error("render: clone", "page", page, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	cloned = cloned.Funcs(template.FuncMap{"T": tr, "langName": langName, "langCode": langCode, "roleName": roleName, "kb": kb, "trim": trimText})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cloned.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render", "page", page, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// baseData — общие данные для шаблонов (с учётом выбранной темы).
func (s *Server) baseData(r *http.Request) map[string]any {
	var user *store.User
	if u := currentUser(r); u != nil {
		user = u
	}
	data := map[string]any{
		"User":       user,
		"IsAdmin":    user != nil && (user.IsRoot || user.Role == "admin"),
		"Version":    version.String(),
		"AppName":    "EOPDS",
		"Theme":      resolveTheme(r),
		"Lang":       resolveLang(r),
		"Langs":      s.i18n.Languages(),
		"FooterText": "Жизнь должна быть наполнена книгами, которые наполнены жизнью.",
		"Nav":        "",
	}
	// Данные футера/статистики (только если БД доступна).
	if st := s.getStore(); st != nil {
		if stats, err := st.Stats(r.Context()); err == nil {
			data["StatsBooks"] = stats.Books
			data["StatsAuthors"] = stats.Authors
		}
		if g, gt, err := st.ListGenres(r.Context(), 1, 0); err == nil {
			_ = g
			data["StatsGenres"] = gt
		}
		if se, stt, err := st.ListSeriesByPrefix(r.Context(), "", 1, 1); err == nil {
			_ = se
			data["StatsSeries"] = stt
		}
		if rb, err := st.RandomBook(r.Context()); err == nil {
			data["RandomBook"] = rb
		}
		if shelf, err := st.RecentBooks(r.Context(), 3); err == nil {
			data["ShelfBooks"] = shelf
		}
	}
	return data
}

// handlePortal — публичная главная (заглушка; позже — стиль старого /web/).
func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	if _, r2 := s.authenticate(w, r); r2 != nil {
		r = r2
	}
	data := s.libData(r, "")
	data["Nav"] = ""
	s.render(w, r, "library_home", data)
}

// handleDashboard — главная админки со статистикой (требует входа).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r)
	data["Installed"] = true
	data["Nav"] = "dashboard"
	if st := s.getStore(); st != nil {
		if stats, err := st.Stats(r.Context()); err != nil {
			s.log.Warn("dashboard: stats", "err", err)
		} else {
			data["Stats"] = stats
		}
		if sum, ok := loadScanSummary(r.Context(), st); ok {
			data["LastScan"] = sum
			if t, err := time.Parse(time.RFC3339, sum.StartedAt); err == nil {
				data["LastScanStart"] = t.Local().Format("02.01.2006 15:04:05")
			}
			data["LastScanDur"] = formatDuration(time.Duration(sum.ElapsedMS) * time.Millisecond)
		}
	}
	s.render(w, r, "home", data)
}

// handleHealth — состояние сервиса (GET /api/health, алиас /healthz).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	db := string(s.cfg.DBType)
	var schemaVersion int

	st := s.getStore()
	if st == nil {
		status = "no-db"
	} else if err := st.Ping(r.Context()); err != nil {
		status = "error"
	} else if v, err := st.SchemaVersion(r.Context()); err == nil {
		schemaVersion = v
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"version":        version.Version,
		"commit":         version.Commit,
		"built_at":       version.BuildTime,
		"schema_version": schemaVersion,
		"db":             db,
	})
}

// handleVersion — версия и сведения о сборке (GET /api/version).
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  version.Version,
		"commit":   version.Commit,
		"built_at": version.BuildTime,
		"go":       goVersion(),
	})
}

// requireInstalled редиректит на /install, если система не установлена.
// Возвращает true, если запрос можно продолжать.
func (s *Server) requireInstalled(w http.ResponseWriter, r *http.Request) bool {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return false
	}
	return true
}

// handleSection рендерит раздел админки (заглушка с общим каркасом).
func (s *Server) handleSection(nav string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireInstalled(w, r) {
			return
		}
		data := s.baseData(r)
		data["Nav"] = nav
		if nav == "scan" {
			if st := s.getStore(); st != nil {
				if paths, err := loadScanPaths(r.Context(), st); err == nil {
					data["ScanPaths"] = paths
				}
				// Итог последнего скана (для блока результата после перезагрузки).
				if sum, ok := loadScanSummary(r.Context(), st); ok {
					data["ScanStatus"] = "done"
					data["Found"] = sum.Found
					data["Created"] = sum.Created
					data["Updated"] = sum.Updated
					data["Parsed"] = sum.Found - sum.ImportErrors
					data["Errors"] = sum.ImportErrors
					data["Skipped"] = sum.Skipped
					if t, terr := time.Parse(time.RFC3339, sum.StartedAt); terr == nil {
						data["StartedAt"] = t.Local().Format("02.01.2006 15:04:05")
					}
					data["Elapsed"] = formatDuration(time.Duration(sum.ElapsedMS) * time.Millisecond)
				}
			}
		}
		if nav == "settings" {
			st := s.getStore()
			if st != nil {
				if users, err := st.ListUsers(r.Context()); err != nil {
					s.log.Warn("settings: list users", "err", err)
				} else {
					data["Users"] = users
				}
				paths, err := loadScanPaths(r.Context(), st)
				if err != nil {
					s.log.Warn("settings: scan paths", "err", err)
				}
				data["ScanPaths"] = paths
			}
		}
		s.render(w, r, nav, data)
	}
}

// writeJSON пишет JSON-ответ.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
