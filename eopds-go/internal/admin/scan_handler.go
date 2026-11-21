package admin

import (
	"net/http"
	"strconv"
	"time"
)

// defaultScanExts — расширения книг по умолчанию для веб-сканирования.
var defaultScanExts = []string{".fb2", ".fb2.zip"}

// handleScanRun запускает сканирование каталогов из настроек (scan.paths)
// в ФОНОВОЙ горутине и сразу возвращает пользователя на /scan.
//
// Скан не привязан к HTTP-запросу: браузер можно закрыть, скан продолжит
// работу (может длиться часы). Прогресс отдаётся через GET /scan/status.
func (s *Server) handleScanRun(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	fullRescan := r.PostFormValue("full") == "1"

	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}

	paths, err := loadScanPaths(r.Context(), st)
	if err != nil {
		s.log.Error("scan: load paths", "err", err)
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}
	var roots []string
	for _, p := range paths {
		if p != "" {
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}

	// Атомарный старт: если уже идёт — просто вернуть на страницу.
	if !s.scan.begin() {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}

	var uid int64
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	s.log.Info("сканирование запущено (фон)", "roots", roots, "user", uid, "full_rescan", fullRescan)
	go s.runScanBg(roots, uid, fullRescan)

	http.Redirect(w, r, "/scan", http.StatusSeeOther)
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
