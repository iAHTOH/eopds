package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

const (
	sessionCookie = "eopds_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// ctxKey — ключ контекста для текущего пользователя.
type ctxKey int

const (
	userCtxKey ctxKey = 1
	csrfCtxKey ctxKey = 2 // CSRF-токен текущей страницы (см. csrf.go)
)

// currentUser достаёт пользователя из контекста запроса.
func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userCtxKey).(*store.User)
	return u
}

// newSessionID генерирует криптостойкий идентификатор сессии.
func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setSessionCookie выставляет cookie сессии.
func setSessionCookie(w http.ResponseWriter, id string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// authenticate проверяет сессию из cookie и подкладывает пользователя в контекст.
// Возвращает true, если пользователь аутентифицирован.
// Дополнительно поддерживает HTTP Basic (нужен для OPDS-клиентов/читалок).
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*store.User, *http.Request) {
	st := s.getStore()
	if st == nil {
		return nil, r
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		u, err := st.GetSessionUser(r.Context(), c.Value)
		if err == nil {
			return u, r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
		}
		if errors.Is(err, store.ErrNotFound) {
			setSessionCookie(w, "", -1) // устаревшая сессия — чистим cookie
		}
		// Пробуем Basic ниже (cookie могла протухнуть, а клиент — с Basic).
	}
	if u := s.authenticateBasic(r); u != nil {
		return u, r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
	}
	return nil, r
}

// authenticateBasic проверяет HTTP Basic-заголовок (для OPDS-клиентов).
// Возвращает пользователя или nil, если учётные данные отсутствуют/неверны.
func (s *Server) authenticateBasic(r *http.Request) *store.User {
	username, password, ok := r.BasicAuth()
	if !ok || username == "" {
		return nil
	}
	st := s.getStore()
	if st == nil {
		return nil
	}
	u, err := st.GetUserByUsername(r.Context(), username)
	if err != nil || !u.IsActive || u.PasswordHash == "" {
		return nil
	}
	if passed, err := auth.VerifyPassword(password, u.PasswordHash); err != nil || !passed {
		return nil
	}
	return u
}

// requireAuth — middleware: пускает только аутентифицированных.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, r2 := s.authenticate(w, r)
		if u == nil {
			nextURL := r.URL.RequestURI()
			http.Redirect(w, r, "/login?next="+url.QueryEscape(nextURL), http.StatusSeeOther)
			return
		}
		// Токен привязан к пользователю — проверяем уже после аутентификации.
		s.csrfGuard(next)(w, r2)
	}
}

// csrfGuard проверяет CSRF на изменяющих запросах. Вызывается после
// аутентификации: для POST без сессии (форма входа, сброс пароля) токен
// сравнивается без привязки к пользователю.
//
// Запросы с HTTP Basic (OPDS-клиенты, curl) не проверяются: они не
// отправляются браузером с амбиентными cookie — CSRF к ним неприменим.
func (s *Server) csrfGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		if csrfExempt(r) || hasBasicAuth(r) {
			next(w, r)
			return
		}
		if !s.checkCSRF(r) {
			s.csrfFailed(w, r)
			return
		}
		next(w, r)
	}
}

// optionalAuth — middleware для публичных страниц библиотеки: аутентифицирует
// при наличии сессии, но НЕ прерывает запрос для гостей (пользователь = nil).
func (s *Server) optionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, r2 := s.authenticate(w, r)
		s.csrfGuard(next)(w, r2)
	}
}

// requireAuthOPDS — middleware для OPDS: гостям отдаём 401 с WWW-Authenticate
// (клиенты-читалки используют HTTP Basic), авторизованных пропускаем.
func (s *Server) requireAuthOPDS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, r2 := s.authenticate(w, r)
		if u == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="EOPDS", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.csrfGuard(next)(w, r2)
	}
}

// requireRole — middleware: требует определённую роль (должен идти после requireAuth).
func (s *Server) requireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u == nil || (!u.IsRoot && u.Role != role) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// handleLoginForm показывает форму входа.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	// Уже вошли — на главную.
	if u, _ := s.authenticate(w, r); u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, "", "")
}

// handleLoginSubmit проверяет логин/пароль и создаёт сессию.
func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tr := s.i18n.Translator(resolveLang(r))
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	next := r.PostFormValue("next")
	// Разрешаем только локальные пути ("/" и не "//host" — защита от open redirect).
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") {
		next = "/"
	}

	st := s.getStore()
	if st == nil {
		s.renderLogin(w, r, tr("login.err.internal"), "")
		return
	}

	u, err := st.GetUserByUsername(r.Context(), username)
	if err != nil || !u.IsActive || u.PasswordHash == "" {
		s.logEvent(r.Context(), store.EventWarn, "auth.login_failed",
			map[string]string{"user": username})
		s.renderLogin(w, r, tr("login.err.credentials"), username)
		return
	}
	ok, err := auth.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		s.logEvent(r.Context(), store.EventWarn, "auth.login_failed",
			map[string]string{"user": username})
		s.renderLogin(w, r, tr("login.err.credentials"), username)
		return
	}

	id, err := newSessionID()
	if err != nil {
		s.log.Error("login: session id", "err", err)
		s.renderLogin(w, r, tr("login.err.internal"), username)
		return
	}
	if err := st.CreateSession(r.Context(), id, u.ID, time.Now().Add(sessionTTL)); err != nil {
		s.log.Error("login: create session", "err", err)
		s.renderLogin(w, r, tr("login.err.internal"), username)
		return
	}
	setSessionCookie(w, id, int(sessionTTL.Seconds()))
	s.log.Info("вход выполнен", "user", u.Username)
	s.logEventUser(r.Context(), u, store.EventInfo, "auth.login", nil)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout удаляет сессию и ведёт на главную.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u != nil {
		s.logEvent(r.Context(), store.EventInfo, "auth.logout", nil)
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if st := s.getStore(); st != nil {
			_ = st.DeleteSession(r.Context(), c.Value)
		}
	}
	setSessionCookie(w, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// renderLogin собирает данные и рендерит страницу входа.
func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, errMsg, username string) {
	data := s.baseData(r)
	data["Title"] = "login"
	data["Error"] = errMsg
	data["Username"] = username
	data["Next"] = r.URL.Query().Get("next")
	// Ссылка «Забыли пароль?» — только когда почта настроена и включена.
	data["MailReady"] = s.mailReady(r.Context())
	// Сообщение после успешного сброса пароля по ссылке из письма.
	if r.URL.Query().Get("reset") == "1" {
		data["Info"] = s.translatorFor(r)("reset.done")
	}
	s.render(w, r, "login", data)
}

// requireMinRole пропускает пользователей с ролью не ниже min
// (rank: reader=1, moderator=2, admin/root=3).
func (s *Server) requireMinRole(min string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u == nil || rank(u.Role, u.IsRoot) < rank(min, false) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// publicForm — обёртка для форм без обязательного входа (вход и сброс
// пароля). CSRF-проверка выполняется до обработчика: токен не связан с
// пользователем, поэтому аутентификация для проверки не нужна.
func (s *Server) publicForm(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.csrfGuard(next)(w, r)
	}
}
