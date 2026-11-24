package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// CSRF-защита форм (double-submit cookie + синхронизация с сессией).
//
// Как работает:
//  1. На каждый показ страницы генерируется случайный токен и выставляется
//     cookie `eopds_csrf` (HttpOnly, SameSite=Lax). Токен попадает в шаблоны
//     через {{csrfField}} (скрытое поле) и {{csrfToken}} (для fetch/JS).
//  2. На любой изменяющий запрос (POST/PUT/PATCH/DELETE) токен из формы или
//     заголовка X-CSRF-Token сверяется с cookie за постоянное время.
//  3. Дополнительно, если пользователь аутентифицирован, токен привязан к его
//     id: встроен в cookie-«пломбу» (см. csrfCookieValue). Смена пользователя
//     в том же браузере инвалидирует старый токен — защита соседних вкладок.
//
// Почему double-submit, а не «токен в сессии»: session-cookie SameSite=Lax
// уже отсекает большинство межсайтовых POST, а double-submit не требует
// ходить в БД на каждый POST и работает для гостевых форм (/login).
//
// Методы GET/HEAD/OPTIONS не проверяются (они не меняют состояние), а токен
// на них генерируется только при рендере страницы (см. ensureCSRFCookie).

const (
	csrfCookieName = "eopds_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfFormField  = "_csrf"
)

// csrfTokenBytes — длина случайного токена (32 байта → 43 символа base64url).
const csrfTokenBytes = 32

// newCSRFToken возвращает криптостойкий случайный токен.
func newCSRFToken() (string, error) {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// csrfCookieValue формирует «пломбу» cookie: токен + привязка к пользователю.
// userID = 0 — гостевая форма (привязки нет).
func csrfCookieValue(token string, userID int64) string {
	if userID == 0 {
		return token + ".g"
	}
	sum := sha256.Sum256([]byte("csrf|" + token + "|" + itoa64(userID)))
	return token + ".u" + base64.RawURLEncoding.EncodeToString(sum[:12])
}

// csrfTokenFromCookie достаёт токен из значения cookie (или "").
func csrfTokenFromCookie(v string) string {
	if i := strings.LastIndexByte(v, '.'); i > 0 {
		return v[:i]
	}
	return ""
}

// csrfCookieValid проверяет, что cookie выдана этому пользователю.
func csrfCookieValid(cookieVal, token string, userID int64) bool {
	return subtle.ConstantTimeCompare([]byte(cookieVal), []byte(csrfCookieValue(token, userID))) == 1
}

// csrfToken возвращает токен для текущего запроса (или "").
func csrfToken(r *http.Request) string {
	if v, ok := r.Context().Value(csrfCtxKey).(string); ok {
		return v
	}
	return ""
}

// setCSRFCookie выдаёт новый токен (если его ещё нет или он чужой).
// Возвращает выданный токен. Вызывается в render и в методах, порождающих
// скрытое поле (_csrf).
func (s *Server) setCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	var userID int64
	if u := currentUser(r); u != nil {
		userID = u.ID
	}
	if c, err := r.Cookie(csrfCookieName); err == nil && c.Value != "" {
		if tok := csrfTokenFromCookie(c.Value); tok != "" && csrfCookieValid(c.Value, tok, userID) {
			return tok
		}
	}
	tok, err := newCSRFToken()
	if err != nil {
		s.log.Error("csrf: токен", "err", err)
		return ""
	}
	s.writeCSRFCookie(w, r, tok, userID)
	return tok
}

// writeCSRFCookie выставляет cookie токена.
func (s *Server) writeCSRFCookie(w http.ResponseWriter, r *http.Request, token string, userID int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfCookieValue(token, userID),
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		// Cookie сессионная: живёт, пока открыт браузер; токен для fetch
		// берётся со страницы, а не из JS-доступа к cookie.
	})
}

// isHTTPS определяет, пришёл ли запрос по HTTPS (с учётом прокси).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	p := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(p, ','); i >= 0 {
		p = p[:i]
	}
	return strings.EqualFold(strings.TrimSpace(p), "https")
}

// csrfExempt — маршруты без CSRF-проверки.
func csrfExempt(r *http.Request) bool {
	// Мастер установки: пользователей ещё нет, токен негде взять.
	if r.Method == http.MethodPost && r.URL.Path == "/install" {
		return true
	}
	return false
}

// hasBasicAuth сообщает, что клиент аутентифицируется по HTTP Basic.
func hasBasicAuth(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Basic ")
}

// checkCSRF сверяет токен из запроса с cookie.
func (s *Server) checkCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	tok := csrfTokenFromCookie(c.Value)
	sent := r.Header.Get(csrfHeaderName)
	if sent == "" {
		_ = r.ParseForm()
		sent = r.PostFormValue(csrfFormField)
	}
	if tok == "" || sent == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(sent)) != 1 {
		return false
	}
	var userID int64
	if u := currentUser(r); u != nil {
		userID = u.ID
	}
	return csrfCookieValid(c.Value, tok, userID)
}

// csrfFailed отдаёт 403 и сообщает об отказе в лог.
func (s *Server) csrfFailed(w http.ResponseWriter, r *http.Request) {
	s.log.Warn("csrf: токен не совпал", "method", r.Method, "path", r.URL.Path,
		"remote", clientIP(r))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("CSRF token missing or invalid. Reload the page and try again.\n"))
}
