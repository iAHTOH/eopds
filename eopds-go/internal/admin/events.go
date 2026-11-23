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
		args = withUserID(args, u.ID)
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
		"book":     book.Title,
		"_book_id": strconv.FormatInt(bookID, 10),
	})
}

// recordRead фиксирует открытие книги в читалке как факт «прочитал/читает»:
// строка в downloads с format='read'. Уникальную популярность это не завышает
// (одна запись на пользователя на книгу). Сообщение системы не пишем — это не
// операционно значимое действие (защита журнала от спама при каждом открытии).
func (s *Server) recordRead(r *http.Request, bookID int64) {
	st := s.getStore()
	if st == nil {
		return
	}
	u := currentUser(r)
	if u == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := st.AddDownload(ctx, bookID, u.ID, "read"); err != nil {
		s.log.Warn("record read", "book", bookID, "err", err)
	}
}

// logEventUser пишет событие от имени явно переданного пользователя
// (для случаев, когда его нет в контексте, напр. сразу после входа).
func (s *Server) logEventUser(ctx context.Context, u *store.User, level, category string, args map[string]string) {
	st := s.getStore()
	if st == nil {
		return
	}
	actor, role := eventActor(u)
	if u != nil {
		args = withUserID(args, u.ID)
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

// withUserID возвращает копию args с id инициатора (для ссылки на профиль в
// журнале). Значения с префиксом «_» служебные и в текст не подставляются.
func withUserID(args map[string]string, uid int64) map[string]string {
	if uid <= 0 {
		return args
	}
	out := make(map[string]string, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["_user_id"] = strconv.FormatInt(uid, 10)
	return out
}

// translatorFor возвращает функцию перевода для языка запроса.
func (s *Server) translatorFor(r *http.Request) func(string) string {
	return s.i18n.Translator(resolveLang(r))
}
