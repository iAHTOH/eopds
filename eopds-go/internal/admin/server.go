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
	"sync"

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
}

// New создаёт сервер и строит наборы шаблонов.
func New(cfg config.Config, log *slog.Logger) (*Server, error) {
	bundle, err := i18n.Load()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: log, pages: make(map[string]*template.Template), i18n: bundle}
	pageFiles := map[string]string{
		"install":  "templates/install.html",
		"home":     "templates/home.html",
		"library":  "templates/library.html",
		"scan":     "templates/scan.html",
		"settings": "templates/settings.html",
		"login":    "templates/login.html",
		"portal":   "templates/portal.html",
	}
	for name, file := range pageFiles {
		base := template.New("layout").Funcs(template.FuncMap{
			"T":        func(k string) string { return k },
			"langName": langName,
			"langCode": langCode,
		})
		tmpl, err := base.ParseFS(templatesFS, "templates/layout.html", file)
		if err != nil {
			return nil, err
		}
		s.pages[name] = tmpl
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
	mux.HandleFunc("GET /library", s.requireAuth(s.handleSection("library")))
	mux.HandleFunc("GET /scan", s.requireAuth(s.handleSection("scan")))
	mux.HandleFunc("GET /settings", s.requireAuth(s.handleSection("settings")))
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
	cloned = cloned.Funcs(template.FuncMap{"T": tr, "langName": langName, "langCode": langCode})
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
	return map[string]any{
		"User":       user,
		"Version":    version.String(),
		"AppName":    "EOPDS",
		"Theme":      resolveTheme(r),
		"Lang":       resolveLang(r),
		"Langs":      s.i18n.Languages(),
		"FooterText": "Жизнь должна быть наполнена книгами, которые наполнены жизнью.",
		"Nav":        "",
	}
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
	data := s.baseData(r)
	data["Nav"] = ""
	s.render(w, r, "portal", data)
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
		s.render(w, r, nav, data)
	}
}

// writeJSON пишет JSON-ответ.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
