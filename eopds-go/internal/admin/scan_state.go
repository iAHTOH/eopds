package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/scansvc"
)

// ScanStateSnapshot — снимок состояния фонового сканирования (для /scan/status).
type ScanStateSnapshot struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"started_at,omitempty"` // RFC3339
	FinishedAt string `json:"finished_at,omitempty"`
	Found      int64  `json:"found"`
	Parsed     int64  `json:"parsed"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	Errors     int64  `json:"errors"`
	Skipped    int64  `json:"skipped"`
	HasRun     bool   `json:"has_run"` // было ли хотя бы одно сканирование
	LastError  string `json:"last_error,omitempty"`
}

// scanState — состояние единственного фонового сканирования.
// Скан живёт независимо от HTTP-запроса (может длиться часами),
// поэтому состояние хранится на сервере и опрашивается из UI.
type scanState struct {
	mu         sync.RWMutex
	running    bool
	startedAt  time.Time
	finishedAt time.Time
	progress   scansvc.Progress
	hasRun     bool
	lastError  string
}

func newScanState() *scanState { return &scanState{} }

// begin отмечает запуск сканирования. false — уже идёт.
func (st *scanState) begin() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.running {
		return false
	}
	st.running = true
	st.hasRun = true
	st.startedAt = time.Now()
	st.finishedAt = time.Time{}
	st.progress = scansvc.Progress{}
	st.lastError = ""
	return true
}

// setProgress обновляет счётчики по мере обработки книг.
func (st *scanState) setProgress(p scansvc.Progress) {
	st.mu.Lock()
	st.progress = p
	st.mu.Unlock()
}

// finish завершает сканирование (в т.ч. с ошибкой).
func (st *scanState) finish(err error) {
	st.mu.Lock()
	st.running = false
	st.finishedAt = time.Now()
	if err != nil {
		st.lastError = err.Error()
	}
	st.mu.Unlock()
}

// snapshot возвращает текущее состояние для UI.
func (st *scanState) snapshot() ScanStateSnapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s := ScanStateSnapshot{
		Running:   st.running,
		Found:     st.progress.Found,
		Parsed:    st.progress.Parsed,
		Skipped:   st.progress.Skipped,
		Created:   st.progress.Created,
		Updated:   st.progress.Updated,
		Errors:    st.progress.Errors,
		HasRun:    st.hasRun,
		LastError: st.lastError,
	}
	if !st.startedAt.IsZero() {
		s.StartedAt = st.startedAt.UTC().Format(time.RFC3339)
	}
	if !st.finishedAt.IsZero() {
		s.FinishedAt = st.finishedAt.UTC().Format(time.RFC3339)
	}
	return s
}

// handleScanStatus отдаёт JSON-снимок фонового сканирования (для поллинга).
func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.scan.snapshot())
}

// runScanBg выполняет сканирование в фоне. Контекст — фоновый (не запроса),
// чтобы скан не обрывался при закрытии браузера.
func (s *Server) runScanBg(roots []string, uid int64, fullRescan bool) {
	// scan.begin уже вызван в handleScanRun
	st := s.getStore()
	if st == nil {
		s.scan.finish(nil)
		return
	}
	ctx := context.Background()

	res, err := scansvc.Run(ctx, st, scansvc.Options{
		Roots:      roots,
		Workers:    0,
		Zip:        true,
		Exts:       defaultScanExts,
		Logger:     logx.New(logx.LevelInfo),
		FullRescan: fullRescan,
		OnProgress: func(p scansvc.Progress) {
			s.scan.setProgress(p)
		},
	})
	if err != nil {
		s.log.Error("scan: run", "err", err)
	}
	s.scan.finish(err)
	s.log.Info("сканирование завершено (фон)",
		"found", res.BooksFound, "created", res.BooksCreated, "updated", res.BooksUpdated,
		"skipped", res.Skipped, "errors", res.ImportErrors, "elapsed", res.Elapsed.String())

	// Сохраняем итог для дашборда (scan.last).
	summary := ScanSummary{
		StartedAt:    res.StartedAt.UTC().Format(time.RFC3339),
		ElapsedMS:    res.Elapsed.Milliseconds(),
		Found:        res.BooksFound,
		Created:      res.BooksCreated,
		Updated:      res.BooksUpdated,
		ImportErrors: res.ImportErrors,
		Skipped:      res.Skipped,
	}
	if enc, jerr := json.Marshal(summary); jerr == nil {
		if serr := st.SetSetting(ctx, scanLastKey, string(enc), uid); serr != nil {
			s.log.Warn("scan: save summary", "err", serr)
		}
	}
}
