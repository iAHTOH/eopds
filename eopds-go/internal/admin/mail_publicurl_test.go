package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// adminHdr — Basic-авторизация root-администратора (CSRF для Basic не нужен).
func adminHdr() map[string]string {
	return map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
}

// normalizePublicURL: каноничный вид и отказы.
func TestNormalizePublicURL(t *testing.T) {
	good := []struct{ in, want string }{
		{"https://library.example.com", "https://library.example.com"},
		{"https://library.example.com/", "https://library.example.com"},
		{"  https://library.example.com/  ", "https://library.example.com"},
		{"https://Library.Example.com", "https://Library.Example.com"},
		{"http://192.168.1.10:8080", "http://192.168.1.10:8080"},
		{"https://example.com/lib/", "https://example.com/lib"},
	}
	for _, c := range good {
		got, err := normalizePublicURL(c.in)
		if err != nil {
			t.Errorf("%q: неожиданная ошибка: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: получено %q, ожидалось %q", c.in, got, c.want)
		}
	}

	bad := []string{
		"",
		"   ",
		"library.example.com",       // без схемы
		"ftp://library.example.com", // не http(s)
		"https://",                  // без хоста
		"https://example.com/?a=1",  // query
		"https://example.com/#top",  // fragment
		"https://exa mple.com",      // пробел внутри
		"javascript:alert(1)",       // не тот протокол
	}
	for _, c := range bad {
		if got, err := normalizePublicURL(c); err == nil {
			t.Errorf("%q: ожидалась ошибка, получено %q", c, got)
		}
	}
}

// Сохранение публичного адреса через форму раздела «Почта».
func TestSaveMailPublicURL(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()

	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"from_addr":  {"lib@example.com"},
		"enabled":    {"1"},
		"public_url": {"https://library.example.com/"},
	}, adminHdr())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); strings.Contains(loc, "err=") {
		t.Fatalf("неожиданная ошибка: %q", loc)
	}
	got, err := st.GetSetting(ctx, settingMailPublicURL)
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	// Завершающая косая черта обрезана при сохранении.
	if got != "https://library.example.com" {
		t.Fatalf("сохранено %q", got)
	}
}

// Неверный адрес: err=public_url, адрес не сохраняется, а валидные настройки
// SMTP — сохраняются.
func TestSaveMailPublicURLInvalid(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()

	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"from_addr":  {"lib@example.com"},
		"public_url": {"not-a-url"},
	}, adminHdr())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "err=public_url") {
		t.Fatalf("ожидалась err=public_url, получено %q", loc)
	}
	if v, _ := st.GetSetting(ctx, settingMailPublicURL); v != "" {
		t.Fatalf("некорректный адрес не должен сохраняться: %q", v)
	}
	if got, _ := st.GetMailSettings(ctx); got.Host != "smtp.example.com" {
		t.Fatalf("настройки SMTP должны сохраниться: %+v", got)
	}

	// В форме есть поле public_url, и введённое значение перенесено
	// в него через параметр pu (после редиректа тела POST уже нет).
	// Фрагмент (#smtp) к серверу не уходит — отбрасываем его, как браузер.
	loc := w.Header().Get("Location")
	if i := strings.IndexByte(loc, '#'); i >= 0 {
		loc = loc[:i]
	}
	w = do(t, srv, http.MethodGet, loc, nil, adminHdr())
	body := w.Body.String()
	if !strings.Contains(body, `name="public_url"`) {
		t.Fatal("в форме нет поля public_url")
	}
	if !strings.Contains(body, `value="not-a-url"`) {
		t.Fatal("введённый адрес потерян после ошибки")
	}
}

// Пустое поле очищает настройку (ссылки снова строятся по хосту запроса).
func TestSaveMailPublicURLClear(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, settingMailPublicURL, "https://old.example.com", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"from_addr":  {"lib@example.com"},
		"public_url": {""},
	}, adminHdr())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	if v, _ := st.GetSetting(ctx, settingMailPublicURL); v != "" {
		t.Fatalf("ожидалось очищение настройки, осталось %q", v)
	}
}

// Ссылка в письме сброса начинается с публичного адреса, а не с хоста запроса.
func TestResetLinkUsesPublicURL(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	rec := newRecordingSender()
	srv.mail.setSender(rec)
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
	if err := st.SetSetting(ctx, settingMailPublicURL, "https://library.example.com", 0); err != nil {
		t.Fatalf("set public url: %v", err)
	}

	csrf := getCSRF(t, srv, "/reset", nil)
	w := doWithCookies(t, srv, http.MethodPost, "/reset",
		url.Values{"email": {"reader@example.com"}}, csrfHdr(csrf), csrfCookieHTTP(csrf))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("reset submit: %d", w.Code)
	}
	waitFor(t, 2*time.Second, func() bool { _, ok := rec.last(); return ok })
	msg, _ := rec.last()
	if msg.Template != mail.TemplatePasswordReset {
		t.Fatalf("неожиданное письмо: %+v", msg)
	}
	if !strings.Contains(msg.Text, "https://library.example.com/reset/confirm?token=") {
		t.Fatalf("ссылка не использует публичный адрес:\n%s", msg.Text)
	}
}

// Без публичного адреса ссылка строится по хосту запроса (прежнее поведение).
func TestResetBaseURLFallsBackToRequestHost(t *testing.T) {
	srv, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/reset", nil)
	if base := srv.resetBaseURL(r); base != "http://example.com" {
		t.Fatalf("ожидался хост запроса, получен %q", base)
	}
}
