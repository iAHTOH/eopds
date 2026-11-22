package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleEventsDownload отдаёт весь журнал сообщений системы как .log
// (текст на языке пользователя). Доступно admin/root и moderator.
func (s *Server) handleEventsDownload(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if !canViewEvents(u) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	max := s.eventsMaxRows(r.Context(), st)
	evs, err := st.SystemEvents(r.Context(), max)
	if err != nil {
		http.Error(w, "events error", http.StatusInternalServerError)
		return
	}
	tr := s.translatorFor(r)
	var sb strings.Builder
	for _, e := range evs {
		ts := time.UnixMilli(e.TS).Local().Format("2006-01-02 15:04:05")
		line := formatEvent(e, tr)
		if strings.TrimSpace(e.Actor) != "" {
			line = actorLabel(e.Actor, e.ActorRole, tr) + " — " + line
		}
		fmt.Fprintf(&sb, "%s [%s] %s\n", ts, eventLevelLabel(e.Level, tr), line)
	}

	name := "system-events-" + time.Now().Format("20060102-150405") + ".log"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	_, _ = w.Write([]byte(sb.String()))
}
