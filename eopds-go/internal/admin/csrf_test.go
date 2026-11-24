package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Форма без CSRF-токена отклоняется: типовая атака — межсайтовый POST.
func TestCSRFMissingTokenRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(t, srv, http.MethodPost, "/reset", url.Values{"email": {"a@example.com"}}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("ожидался 403, получено %d", w.Code)
	}
}

// Токен из формы в паре с cookie проходит (double-submit).
func TestCSRFValidTokenAccepted(t *testing.T) {
	srv, _ := newTestServer(t)
	tok := getCSRF(t, srv, "/reset", nil)
	w := doWithCookies(t, srv, http.MethodPost, "/reset",
		url.Values{"email": {"a@example.com"}, csrfFormField: {tok}}, nil, csrfCookieHTTP(tok))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался редирект, получено %d: %s", w.Code, w.Body.String())
	}
}

// Чужой токен (cookie от одной страницы, значение от другой) не проходит.
func TestCSRFMismatchedTokenRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	own := getCSRF(t, srv, "/reset", nil)
	other := getCSRF(t, srv, "/reset", nil)
	if own == other {
		t.Fatal("токены должны быть разными")
	}
	w := doWithCookies(t, srv, http.MethodPost, "/reset",
		url.Values{"email": {"a@example.com"}}, csrfHdr(other), csrfCookieHTTP(own))
	if w.Code != http.StatusForbidden {
		t.Fatalf("ожидался 403, получено %d", w.Code)
	}
}

// Заголовок X-CSRF-Token работает без скрытого поля (для fetch/JS).
func TestCSRFHeaderAccepted(t *testing.T) {
	srv, _ := newTestServer(t)
	tok := getCSRF(t, srv, "/reset", nil)
	w := doWithCookies(t, srv, http.MethodPost, "/reset",
		url.Values{"email": {"a@example.com"}}, csrfHdr(tok), csrfCookieHTTP(tok))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался редирект, получено %d", w.Code)
	}
}

// Запросы с HTTP Basic (OPDS-клиенты, curl) не проверяются: браузер не
// отправляет Basic автоматически, CSRF к ним неприменим.
func TestCSRFBasicAuthExempt(t *testing.T) {
	srv, _ := newTestServer(t)
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	w := do(t, srv, http.MethodPost, "/settings/mail/clear", url.Values{}, hdr)
	if w.Code == http.StatusForbidden {
		t.Fatal("запросы с Basic не должны отклоняться CSRF")
	}
}

// На странице есть скрытое поле токена, и оно совпадает с cookie.
func TestCSRFFieldRendered(t *testing.T) {
	srv, st := newTestServer(t)
	// Форма /reset показывается только при настроенной почте.
	if err := st.SaveMailSettings(context.Background(), store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("mail settings: %v", err)
	}
	w := do(t, srv, http.MethodGet, "/reset", nil, nil)
	body := w.Body.String()
	if !strings.Contains(body, `name="`+csrfFormField+`"`) {
		t.Fatal("в форме нет скрытого поля CSRF")
	}
	if !strings.Contains(body, `value="`+csrfFromResp(t, w)+`"`) {
		t.Fatal("значение поля не совпадает с выданным токеном")
	}
}

// GET не требует токена и не меняет состояние.
func TestCSRFGetNotChecked(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(t, srv, http.MethodGet, "/reset", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получено %d", w.Code)
	}
}
