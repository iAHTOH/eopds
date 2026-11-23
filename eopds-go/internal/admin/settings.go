package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	Skipped      int64  `json:"skipped"`       // пропущено (не изменились)
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
	http.Redirect(w, r, "/settings?sec=scan&lang="+resolveLang(r)+"#scan", http.StatusSeeOther)
}

// settingsSectionAllowed определяет активный раздел «Настроек» (?sec=),
// проверяя право доступа текущего пользователя. Неизвестный или запрещённый
// раздел заменяется первым доступным (или "general").
func settingsSectionAllowed(u *store.User, want string) string {
	groups := settingsNav(u)
	allowed := make(map[string]bool, 8)
	var first string
	for _, g := range groups {
		for _, it := range g.Items {
			allowed[it.Sec] = true
			if first == "" {
				first = it.Sec
			}
		}
	}
	if want != "" && allowed[want] {
		return want
	}
	if first != "" {
		return first
	}
	return "general"
}

// settingsData наполняет данные шаблона раздела «Настроек»:
// активный раздел, сгруппированное меню и содержимое выбранного раздела.
func (s *Server) settingsData(r *http.Request, data map[string]any) {
	u := currentUser(r)
	sec := settingsSectionAllowed(u, r.URL.Query().Get("sec"))
	data["Sec"] = sec
	data["NavGroups"] = settingsNav(u)
	if u != nil {
		data["IsAdmin"] = rank(u.Role, u.IsRoot) >= 3
	}

	st := s.getStore()
	if st == nil {
		return
	}
	ctx := r.Context()

	switch sec {
	case "users":
		users, err := st.ListUsers(ctx)
		if err != nil {
			s.log.Warn("settings: list users", "err", err)
		}
		data["Users"] = users

	case "scan":
		paths, err := loadScanPaths(ctx, st)
		if err != nil {
			s.log.Warn("settings: scan paths", "err", err)
		}
		data["ScanPaths"] = paths

		if sum, ok := loadScanSummary(ctx, st); ok {
			data["ScanStatus"] = "done"
			data["Found"] = sum.Found
			data["Created"] = sum.Created
			data["Updated"] = sum.Updated
			data["Parsed"] = sum.Found - sum.ImportErrors
			data["Errors"] = sum.ImportErrors
			data["Skipped"] = sum.Skipped
			if t, perr := time.Parse(time.RFC3339, sum.StartedAt); perr == nil {
				data["StartedAt"] = t.Local().Format("02.01.2006 15:04:05")
			} else {
				data["StartedAt"] = sum.StartedAt
			}
			data["Elapsed"] = formatDuration(time.Duration(sum.ElapsedMS) * time.Millisecond)
		}
	}
}

// handleClearLibrary очищает каталог книг (опасная зона раздела «Сканирование»).
// Требует подтверждения (confirm=yes); после очистки сбрасывает итог сканирования.
func (s *Server) handleClearLibrary(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	back := "/settings?sec=scan&lang=" + resolveLang(r) + "#danger"
	if r.PostFormValue("confirm") != "yes" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	ctx := r.Context()

	res, err := st.ClearLibrary(ctx)
	if err != nil {
		s.log.Error("settings: clear library", "err", err)
		http.Error(w, "clear failed", http.StatusInternalServerError)
		return
	}

	var uid int64
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	if err := st.SetSetting(ctx, scanLastKey, "", uid); err != nil {
		s.log.Warn("settings: reset scan summary", "err", err)
	}
	s.logEvent(ctx, store.EventWarn, "lib.clear", map[string]string{
		"books": strconv.FormatInt(res.Books, 10),
	})
	s.log.Info("библиотека очищена", "books", res.Books, "authors", res.Authors)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
