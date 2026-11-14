package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
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

const userCtxKey ctxKey = 1

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
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*store.User, *http.Request) {
	st := s.getStore()
	if st == nil {
		return nil, r
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, r
	}
	u, err := st.GetSessionUser(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			setSessionCookie(w, "", -1) // устаревшая сессия — чистим cookie
		}
		return nil, r
	}
	return u, r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
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
		next(w, r2)
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
	s.renderLogin(w, r, "", r.URL.Query().Get("next"))
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
	if next == "" || next[0] != '/' {
		next = "/"
	}

	st := s.getStore()
	if st == nil {
		s.renderLogin(w, r, tr("login.err.internal"), "")
		return
	}

	u, err := st.GetUserByUsername(r.Context(), username)
	if err != nil || !u.IsActive || u.PasswordHash == "" {
		s.renderLogin(w, r, tr("login.err.credentials"), username)
		return
	}
	ok, err := auth.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
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
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout удаляет сессию и ведёт на главную.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
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
	s.render(w, r, "login", data)
}
