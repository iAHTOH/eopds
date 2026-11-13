// Package admin — HTTP-сервер административной панели и мастера установки.
//
// Стек — чистый stdlib: net/http + html/template + ванильный CSS. CGO выключен.
//
// Логика маршрутов:
//   - если БД ещё не сконфигурирована/недоступна или не установлена → мастер
//     установки (/install);
//   - после установки — редирект на главную админки (заглушка на этом этапе).
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"sync"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
	"github.com/iAHTOH/eopds/eopds-go/internal/version"
)

// Server — HTTP-сервер админки/установщика.
type Server struct {
	cfg   config.Config
	log   *slog.Logger
	mu    sync.RWMutex
	store store.Store // nil, пока не установлено/не подключено

	tmpl *template.Template
}

// New создаёт сервер и парсит встроенные шаблоны.
func New(cfg config.Config, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, log: log}
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.tmpl = tmpl
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
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /install", s.handleInstallForm)
	mux.HandleFunc("POST /install", s.handleInstallSubmit)
	mux.HandleFunc("GET /healthz", s.handleHealth) // простой алиас
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	return mux
}

// isInstalled проверяет, пройден ли мастер установки.
// Пустая БД/ошибка → false (показываем установщик).
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

// render рендерит шаблон в ответ.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "tmpl", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// baseData — общие данные для шаблонов.
func (s *Server) baseData() map[string]any {
	return map[string]any{
		"Version": version.String(),
		"AppName": "EOPDS",
	}
}

// handleHome — заглушка главной админки.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	data := s.baseData()
	data["Installed"] = true
	s.render(w, "home.html", data)
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

// writeJSON пишет JSON-ответ.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// errUser — ошибка, которую можно показать пользователю на форме.
type formError string

func (e formError) Error() string { return string(e) }

// asFormError извлекает текст для формы.
func asFormError(err error) string {
	var fe formError
	if errors.As(err, &fe) {
		return string(fe)
	}
	return "Внутренняя ошибка. Проверьте журнал сервера."
}
