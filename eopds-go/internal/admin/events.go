package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Ключ настройки и дефолт ёмкости журнала сообщений системы.
const (
	settingEventsMaxRows = "events.max_rows"
	defaultEventsMaxRows = 1000
	// displayEventsLimit — сколько сообщений показывать в дашборде.
	displayEventsLimit = 100
)

// eventsMaxRows читает настройку events.max_rows (дефолт defaultEventsMaxRows).
func (s *Server) eventsMaxRows(ctx context.Context, st store.Store) int {
	if v, err := st.GetSetting(ctx, settingEventsMaxRows); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && n > 0 {
			return n
		}
	}
	return defaultEventsMaxRows
}

// eventActor возвращает имя и роль пользователя для лога событий.
// Пустой пользователь — система (actor="", role="").
func eventActor(u *store.User) (name, role string) {
	if u == nil {
		return "", ""
	}
	role = u.Role
	if u.IsRoot {
		role = "admin"
	}
	return u.Username, role
}

// logEvent пишет сообщение системы. Не должно ломать основной запрос:
// ошибки только логируются в stderr-логгер. uid — id инициатора (0 — система).
func (s *Server) logEvent(ctx context.Context, level, category string, args map[string]string) {
	st := s.getStore()
	if st == nil {
		return
	}
	actor, role := "", ""
	if u, _ := ctx.Value(userCtxKey).(*store.User); u != nil {
		actor, role = eventActor(u)
	}
	e := store.SystemEvent{
		Level:     level,
		Category:  category,
		Actor:     actor,
		ActorRole: role,
		Args:      args,
	}
	if err := st.AddSystemEvent(ctx, e, s.eventsMaxRows(ctx, st)); err != nil {
		s.log.Warn("system event", "category", category, "err", err)
	}
}

// recordDownload фиксирует скачивание: таблицу downloads (аналитика) и
// сообщение системы (book.download). bookID — id книги, fmt — запрошенный формат.
func (s *Server) recordDownload(r *http.Request, book store.LibraryBook, bookID int64, format string) {
	st := s.getStore()
	if st == nil {
		return
	}
	var uid int64
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	ctx := context.WithoutCancel(r.Context())
	if err := st.AddDownload(ctx, bookID, uid, format); err != nil {
		s.log.Warn("record download", "book", bookID, "err", err)
	}
	s.logEvent(ctx, store.EventInfo, "book.download", map[string]string{
		"book": book.Title,
	})
}

// logEventUser пишет событие от имени явно переданного пользователя
// (для случаев, когда его нет в контексте, напр. сразу после входа).
func (s *Server) logEventUser(ctx context.Context, u *store.User, level, category string, args map[string]string) {
	st := s.getStore()
	if st == nil {
		return
	}
	actor, role := eventActor(u)
	e := store.SystemEvent{
		Level:     level,
		Category:  category,
		Actor:     actor,
		ActorRole: role,
		Args:      args,
	}
	if err := st.AddSystemEvent(ctx, e, s.eventsMaxRows(ctx, st)); err != nil {
		s.log.Warn("system event", "category", category, "err", err)
	}
}

// translatorFor возвращает функцию перевода для языка запроса.
func (s *Server) translatorFor(r *http.Request) func(string) string {
	return s.i18n.Translator(resolveLang(r))
}
