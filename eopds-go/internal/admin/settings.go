package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// scanPathsKey — ключ настройки со списком каталогов для сканирования (JSON).
const scanPathsKey = "scan.paths"

// scanLastKey — ключ настройки с итогом последнего сканирования (JSON ScanSummary).
const scanLastKey = "scan.last"

// ScanSummary — сохранённый итог последнего сканирования (для дашборда).
type ScanSummary struct {
	StartedAt    string `json:"started_at"`    // RFC3339
	ElapsedMS    int64  `json:"elapsed_ms"`    // длительность в мс
	Found        int64  `json:"found"`         // найдено книг
	Created      int64  `json:"created"`       // добавлено
	Updated      int64  `json:"updated"`       // обновлено
	ImportErrors int64  `json:"import_errors"` // ошибок импорта
	Skipped      int64  `json:"skipped"`       // пропущено (неизменившиеся)
}

// loadScanSummary возвращает итог последнего сканирования.
// ok=false, если сканирование ещё не выполнялось.
func loadScanSummary(ctx context.Context, st store.Store) (ScanSummary, bool) {
	raw, err := st.GetSetting(ctx, scanLastKey)
	if err != nil || raw == "" {
		return ScanSummary{}, false
	}
	var s ScanSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return ScanSummary{}, false
	}
	return s, true
}

// loadScanPaths возвращает список каталогов сканирования из настроек.
// Пустой список, если ещё не задан.
func loadScanPaths(ctx context.Context, st store.Store) ([]string, error) {
	raw, err := st.GetSetting(ctx, scanPathsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	var paths []string
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			// повреждённое значение — отдаём пустой список
			return []string{}, nil
		}
	}
	// минимум одно поле для формы
	if len(paths) == 0 {
		paths = []string{""}
	}
	return paths, nil
}

// handleSaveScanPaths сохраняет список каталогов сканирования.
func (s *Server) handleSaveScanPaths(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	u := currentUser(r)
	var uid int64
	if u != nil {
		uid = u.ID
	}

	// Берём все поля path, чистим пустые/дубликаты.
	var paths []string
	seen := map[string]bool{}
	for _, p := range r.PostForm["path"] {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}

	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	enc, _ := json.Marshal(paths)
	if err := st.SetSetting(r.Context(), scanPathsKey, string(enc), uid); err != nil {
		s.log.Error("settings: save scan paths", "err", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	// Дублируем в config.json — единый источник настроек для перезапуска.
	s.mu.Lock()
	s.cfg.ScanPaths = paths
	cfgSnap := s.cfg
	s.mu.Unlock()
	if err := config.SaveConfigFile(cfgSnap.ConfigDir, cfgSnap); err != nil {
		s.log.Warn("settings: не удалось сохранить config.json", "err", err)
	}

	s.log.Info("каталоги сканирования сохранены", "count", len(paths))
	s.logEvent(r.Context(), store.EventInfo, "settings.scan_dirs", map[string]string{
		"count": itoa64(int64(len(paths))),
	})
	http.Redirect(w, r, "/settings?sec=scan&lang="+resolveLang(r)+"#scan", http.StatusSeeOther)
}

// handleClearLibrary полностью очищает каталог книг (кнопка в настройках).
func (s *Server) handleClearLibrary(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Защита от случайной очистки: требуется подтверждение.
	if r.PostFormValue("confirm") != "yes" {
		http.Redirect(w, r, "/settings?sec=scan&lang="+resolveLang(r)+"#danger", http.StatusSeeOther)
		return
	}
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	res, err := st.ClearLibrary(r.Context())
	if err != nil {
		s.log.Error("settings: clear library", "err", err)
		http.Error(w, "clear failed", http.StatusInternalServerError)
		return
	}
	_ = st.SetSetting(r.Context(), scanLastKey, "", 0) // сброс итога последнего скана
	s.log.Info("библиотека очищена",
		"books", res.Books, "authors", res.Authors,
		"series", res.Series, "genres", res.Genres, "catalogs", res.Catalogs)
	s.logEvent(r.Context(), store.EventWarn, "lib.clear", map[string]string{
		"books": itoa64(res.Books),
	})
	http.Redirect(w, r, "/settings?sec=scan&lang="+resolveLang(r)+"#danger", http.StatusSeeOther)
}

// settingsSectionAllowed сообщает, доступен ли раздел ?sec=<sec> роли.
// Возвращает канонический sec (если он есть в меню) и доступность.
func (s *Server) settingsSectionAllowed(u *store.User, sec string) (string, bool) {
	if sec == "" {
		// Дефолтный раздел: первый доступный в меню.
		groups := settingsNav(u)
		for _, g := range groups {
			if len(g.Items) > 0 {
				return g.Items[0].Sec, true
			}
		}
		return "", false
	}
	for _, g := range settingsNav(u) {
		for _, it := range g.Items {
			if it.Sec == sec {
				return sec, true
			}
		}
	}
	return "", false
}

// settingsData наполняет data для раздела «Настройки» (навигация + контент sec).
func (s *Server) settingsData(r *http.Request, data map[string]any) {
	u := currentUser(r)
	if u == nil {
		return
	}
	st := s.getStore()
	if st == nil {
		return
	}

	sec, ok := s.settingsSectionAllowed(u, r.URL.Query().Get("sec"))
	// Нет доступа/пустое меню — не должно случаться (роут защищён ролью),
	// но на всякий случай покажем первую доступную группу.
	if !ok {
		sec = "general"
	}
	data["Sec"] = sec
	data["NavGroups"] = settingsNav(u)

	// Данные под конкретный раздел.
	switch sec {
	case "users":
		if users, err := st.ListUsers(r.Context()); err != nil {
			s.log.Warn("settings: list users", "err", err)
		} else {
			data["Users"] = users
		}
	case "scan":
		if paths, err := loadScanPaths(r.Context(), st); err != nil {
			s.log.Warn("settings: scan paths", "err", err)
		} else {
			data["ScanPaths"] = paths
		}
	}
}
