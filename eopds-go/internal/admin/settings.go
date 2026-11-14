package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
	s.log.Info("каталоги сканирования сохранены", "count", len(paths))
	http.Redirect(w, r, "/settings?lang="+resolveLang(r)+"#scan", http.StatusSeeOther)
}
