package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/scansvc"
)

// defaultScanExts — расширения книг по умолчанию для веб-сканирования.
var defaultScanExts = []string{".fb2", ".fb2.zip"}

// handleScanRun запускает сканирование каталогов из настроек (scan.paths)
// и пишет найденные книги в БД. Только admin/root, одна задача за раз.
func (s *Server) handleScanRun(w http.ResponseWriter, r *http.Request) {
	if !s.scanning.CompareAndSwap(false, true) {
		s.renderScanPage(w, r, "warning", map[string]any{"busy": true})
		return
	}
	defer s.scanning.Store(false)

	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}

	paths, err := loadScanPaths(r.Context(), st)
	if err != nil {
		s.log.Error("scan: load paths", "err", err)
		s.renderScanPage(w, r, "error", nil)
		return
	}
	// Отбрасываем пустые.
	var roots []string
	for _, p := range paths {
		if p != "" {
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		s.renderScanPage(w, r, "no_paths", nil)
		return
	}

	s.log.Info("сканирование запущено", "roots", roots)
	res, err := scansvc.Run(r.Context(), st, scansvc.Options{
		Roots:   roots,
		Workers: 0, // NumCPU по умолчанию внутри walk
		Zip:     true,
		Exts:    defaultScanExts,
		Logger:  logx.New(logx.LevelInfo),
	})
	if err != nil {
		s.log.Error("scan: run", "err", err)
		s.renderScanPage(w, r, "error", nil)
		return
	}
	s.log.Info("сканирование завершено",
		"found", res.BooksFound, "created", res.BooksCreated, "updated", res.BooksUpdated,
		"errors", res.ImportErrors, "elapsed", res.Elapsed.String())

	// Сохраняем итог для дашборда (scan.last).
	summary := ScanSummary{
		StartedAt:    res.StartedAt.UTC().Format(time.RFC3339),
		ElapsedMS:    res.Elapsed.Milliseconds(),
		Found:        res.BooksFound,
		Created:      res.BooksCreated,
		Updated:      res.BooksUpdated,
		ImportErrors: res.ImportErrors,
	}
	if enc, jerr := json.Marshal(summary); jerr == nil {
		var uid int64
		if u := currentUser(r); u != nil {
			uid = u.ID
		}
		if serr := st.SetSetting(r.Context(), scanLastKey, string(enc), uid); serr != nil {
			s.log.Warn("scan: save summary", "err", serr)
		}
	}

	s.renderScanPage(w, r, "done", map[string]any{
		"Found":     res.BooksFound,
		"Created":   res.BooksCreated,
		"Updated":   res.BooksUpdated,
		"Parsed":    res.BooksParsed,
		"Errors":    res.ImportErrors,
		"Roots":     res.Roots,
		"StartedAt": res.StartedAt.Format("02.01.2006 15:04:05"),
		"Elapsed":   formatDuration(res.Elapsed),
	})
}

// formatDuration форматирует длительность по-человечески (ч/м/с).
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return strconv.Itoa(h) + " ч " + strconv.Itoa(m) + " мин " + strconv.Itoa(sec) + " с"
	}
	if m > 0 {
		return strconv.Itoa(m) + " мин " + strconv.Itoa(sec) + " с"
	}
	return strconv.Itoa(sec) + " с"
}

// renderScanPage рендерит /scan с сообщением статуса.
func (s *Server) renderScanPage(w http.ResponseWriter, r *http.Request, status string, extra map[string]any) {
	data := s.baseData(r)
	data["Nav"] = "scan"
	data["ScanStatus"] = status
	for k, v := range extra {
		data[k] = v
	}
	// Текущие каталоги для показа.
	if st := s.getStore(); st != nil {
		if paths, err := loadScanPaths(r.Context(), st); err == nil {
			data["ScanPaths"] = paths
		}
	}
	s.render(w, r, "scan", data)
}
