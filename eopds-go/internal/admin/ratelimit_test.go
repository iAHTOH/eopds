package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

func TestRateLimiterWindow(t *testing.T) {
	l := newRateLimiter(3, time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a@example.com"); !ok {
			t.Fatalf("попытка %d должна пройти", i+1)
		}
	}
	ok, wait := l.Allow("a@example.com")
	if ok {
		t.Fatal("четвёртая попытка должна быть отклонена")
	}
	if wait <= 0 || wait > time.Hour {
		t.Fatalf("ожидалось ожидание в пределах часа, получено %v", wait)
	}
	// Окно скользящее: ждать нужно до освобождения одного слота, а не
	// полные 3600 с от момента отказа.
	l2 := newRateLimiter(2, time.Hour)
	l2.entries["k"] = []time.Time{
		time.Now().Add(-50 * time.Minute),
		time.Now().Add(-10 * time.Minute),
	}
	ok, wait = l2.Allow("k")
	if ok {
		t.Fatal("лимит должен быть исчерпан")
	}
	if wait < 5*time.Minute || wait > 15*time.Minute {
		t.Fatalf("ожидалось ~10 минут до освобождения слота, получено %v", wait)
	}
	// Другой ключ не затронут.
	if ok, _ := l.Allow("b@example.com"); !ok {
		t.Fatal("другой ключ должен быть разрешён")
	}
	// Ключ не зависит от регистра и пробелов.
	if ok, _ := l.Allow("  A@Example.com "); ok {
		t.Fatal("ключ должен нормализоваться: лимит уже исчерпан")
	}
}

func TestRateLimiterExpires(t *testing.T) {
	l := newRateLimiter(1, 20*time.Millisecond)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("первая попытка должна пройти")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("вторая попытка в окне должна быть отклонена")
	}
	time.Sleep(30 * time.Millisecond)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("после окна попытка должна пройти")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		hdr    map[string]string
		want   string
	}{
		{"remote addr", "192.0.2.7:5555", nil, "192.0.2.7"},
		{"xff один", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"xff цепочка", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "10.0.0.1, 203.0.113.9"}, "203.0.113.9"},
		{"x-real-ip", "127.0.0.1:1", map[string]string{"X-Real-IP": "203.0.113.10"}, "203.0.113.10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, ожидалось %q", got, c.want)
			}
		})
	}
}

// seedResetEnv готовит сервер к сценарию сброса: пользователь, настройки
// почты, «пустой» резолвер и чистые лимитеры.
func seedResetEnv(t *testing.T) (*Server, store.Store) {
	t.Helper()
	srv, st := newTestServer(t)
	ctx := context.Background()
	srv.mail.setSender(newRecordingSender())
	srv.mailCheck = newStubChecker(t)
	srv.resetByEmail.Reset()
	srv.resetByIP.Reset()
	hash, _ := auth.HashPassword("password")
	createUserWithEmail(t, st, "reader", "reader@example.com", hash)
	if err := st.SaveMailSettings(ctx, store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("mail settings: %v", err)
	}
	return srv, st
}

// Превышение лимита на адрес не отправляет письмо, но ответ — тот же
// («письмо отправлено»), чтобы не раскрывать состояние лимита.
func TestResetRateLimited(t *testing.T) {
	srv, st := seedResetEnv(t)
	rec := srv.mail.sender().(*recordingSender)

	csrf := getCSRF(t, srv, "/reset", nil)
	send := func() string {
		w := doWithCookies(t, srv, http.MethodPost, "/reset",
			url.Values{"email": {"reader@example.com"}}, csrfHdr(csrf), csrfCookieHTTP(csrf))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status %d", w.Code)
		}
		return w.Header().Get("Location")
	}
	for i := 0; i < resetPerEmailLimit; i++ {
		if loc := send(); !strings.Contains(loc, "sent=1") {
			t.Fatalf("запрос %d: ожидался sent=1, получено %q", i+1, loc)
		}
	}
	// Лимит исчерпан: ответ прежний, но письма нет и токена тоже.
	before := rec.count()
	if loc := send(); !strings.Contains(loc, "sent=1") {
		t.Fatalf("ответ на превышение лимита не должен отличаться: %q", loc)
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count() != before {
		t.Fatal("при превышении лимита письмо отправлено быть не должно")
	}
	// Событие о срабатывании видно в «Сообщениях системы».
	evts, err := st.SystemEvents(context.Background(), 20)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	found := false
	for _, e := range evts {
		if e.Category == "mail.reset_rate_limited" {
			found = true
			for _, v := range e.Args {
				if strings.Contains(v, "reader@example.com") {
					t.Errorf("адрес в событии должен быть замаскирован: %v", e.Args)
				}
			}
		}
	}
	if !found {
		t.Fatal("нет события mail.reset_rate_limited")
	}
}

// Лимит по IP срабатывает и для разных адресов с одного клиента.
func TestResetRateLimitedByIP(t *testing.T) {
	srv, _ := seedResetEnv(t)
	rec := srv.mail.sender().(*recordingSender)
	// Тот же IP у всех запросов.
	csrf := getCSRF(t, srv, "/reset", map[string]string{"X-Forwarded-For": "203.0.113.77"})

	send := func(email string) {
		hdr := map[string]string{"X-Forwarded-For": "203.0.113.77", csrfHeaderName: csrf}
		w := doWithCookies(t, srv, http.MethodPost, "/reset", url.Values{"email": {email}}, hdr, csrfCookieHTTP(csrf))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status %d", w.Code)
		}
	}
	// Разные адреса обходят лимит на адрес, но упираются в лимит по IP.
	for i := 0; i < resetPerIPLimit; i++ {
		send(fmt.Sprintf("user%d@example.com", i))
	}
	time.Sleep(50 * time.Millisecond)
	before := rec.count()
	// Новый адрес, но тот же IP — лимит исчерпан.
	send("other@example.com")
	time.Sleep(50 * time.Millisecond)
	if rec.count() != before {
		t.Fatal("при превышении лимита по IP письмо отправлено быть не должно")
	}
}

// --- CSRF: помощники для тестов -------------------------------------------

// csrfFromResp достаёт CSRF-токен из cookie ответа (выдаётся при показе формы).
func csrfFromResp(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			if tok := csrfTokenFromCookie(c.Value); tok != "" {
				return tok
			}
		}
	}
	t.Fatalf("в ответе нет cookie %s", csrfCookieName)
	return ""
}

// csrfHdr возвращает заголовки с токеном CSRF (плюс cookie того же токена:
// double-submit — браузер присылает обе половины, тест делает так же).
func csrfHdr(token string) map[string]string {
	return map[string]string{csrfHeaderName: token}
}

// csrfCookieHTTP создаёт cookie с CSRF-токеном для подстановки в запрос.
func csrfCookieHTTP(token string) *http.Cookie {
	return &http.Cookie{Name: csrfCookieName, Value: csrfCookieValue(token, 0)}
}

// doWithCookies выполняет запрос с клиентскими cookie.
func doWithCookies(t *testing.T, srv *Server, method, target string, form url.Values,
	hdr map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

// getCSRF открывает страницу и возвращает токен CSRF, выданный вместе с ней.
func getCSRF(t *testing.T, srv *Server, target string, hdr map[string]string) string {
	t.Helper()
	w := do(t, srv, http.MethodGet, target, nil, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", target, w.Code)
	}
	return csrfFromResp(t, w)
}
