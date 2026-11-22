package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// eventView — готовая к отображению строка сообщения системы.
type eventView struct {
	Time      string // локальное время
	Level     string // info|warn|error (класс для стилей)
	ActorName string // имя инициатора ('' — системное событие)
	ActorRole string // admin|moderator|reader|'' ; '' — системное
	ActorText string // локализованная подпись роли ("администратор", ...)
	Message   string // текст события (без actor'а)
}

// eventViews превращает события в отображаемые строки на языке lang.
// tr — функция перевода (i18n). Роль actor'а оформляется отдельно в шаблоне
// (по полям), поэтому здесь она не входит в текст сообщения.
func eventViews(evs []store.SystemEvent, tr func(string) string, loc *time.Location) []eventView {
	if loc == nil {
		loc = time.Local
	}
	out := make([]eventView, 0, len(evs))
	for _, e := range evs {
		name := e.Actor
		role := e.ActorRole
		roleText := ""
		if strings.TrimSpace(name) == "" {
			role = ""
		} else if role != "" {
			roleText = tr("events.role." + role)
		}
		out = append(out, eventView{
			Time:      time.UnixMilli(e.TS).In(loc).Format("02.01.2006 15:04:05"),
			Level:     e.Level,
			ActorName: name,
			ActorRole: role,
			ActorText: roleText,
			Message:   formatEvent(e, tr),
		})
	}
	return out
}

// formatEvent подставляет args в переведённый шаблон ev.<category>.
// Actor в текст не входит — он выводится отдельным элементом в шаблоне.
func formatEvent(e store.SystemEvent, tr func(string) string) string {
	tmpl := tr("ev." + e.Category)

	repl := map[string]string{
		"user": e.Actor,
	}
	// Данные события.
	for k, v := range e.Args {
		repl[k] = v
	}
	// Для login_failed actor может быть пустым — подставим имя из args.user.
	if e.Args != nil {
		if u, ok := e.Args["user"]; ok {
			repl["user"] = u
		}
	}
	for k, v := range repl {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

// actorLabel формирует строку "роль Имя" для подстановки {actor}.
// Для пустого actor — «система».
func actorLabel(actor, role string, tr func(string) string) string {
	if strings.TrimSpace(actor) == "" {
		return tr("events.role.system")
	}
	if role == "" {
		return actor
	}
	return tr("events.role."+role) + " " + actor
}

// eventLevelLabel — локализованное имя уровня.
func eventLevelLabel(level string, tr func(string) string) string {
	switch level {
	case store.EventWarn:
		return tr("events.level.warn")
	case store.EventError:
		return tr("events.level.error")
	default:
		return tr("events.level.info")
	}
}

// canViewEvents — сообщения системы видят admin (root) и moderator.
func canViewEvents(u *store.User) bool {
	if u == nil {
		return false
	}
	return u.IsRoot || u.Role == "admin" || u.Role == "moderator"
}

// addEventsData наполняет data данными блока «Сообщения системы»
// (только для admin/moderator). Для остальных модуль не выводится.
func (s *Server) addEventsData(r *http.Request, data map[string]any, st store.Store) {
	u := currentUser(r)
	if !canViewEvents(u) {
		return
	}
	tr := s.translatorFor(r)
	evs, err := st.SystemEvents(r.Context(), displayEventsLimit)
	if err != nil {
		return
	}
	data["ShowEvents"] = true
	data["Events"] = eventViews(evs, tr, time.Local)
	data["EventsLimit"] = displayEventsLimit
	data["EventsTotal"], _ = st.CountSystemEvents(r.Context())
}
