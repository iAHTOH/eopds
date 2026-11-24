package admin

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/emailcheck"
	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// recordingSender — подменный отправитель почты: запоминает письма.
type recordingSender struct {
	mu  chan struct{}
	got []mail.Message
	err error
}

func newRecordingSender() *recordingSender {
	return &recordingSender{mu: make(chan struct{}, 1)}
}

func (r *recordingSender) Send(_ context.Context, _ mail.Settings, m mail.Message) mail.Result {
	select {
	case r.mu <- struct{}{}:
		r.got = append(r.got, m)
		<-r.mu
	default:
	}
	return mail.Result{MessageID: "test-id", Err: r.err}
}

func (r *recordingSender) last() (mail.Message, bool) {
	select {
	case r.mu <- struct{}{}:
		defer func() { <-r.mu }()
		if len(r.got) == 0 {
			return mail.Message{}, false
		}
		return r.got[len(r.got)-1], true
	default:
		return mail.Message{}, false
	}
}

// count возвращает число отправленных писем.
func (r *recordingSender) count() int {
	select {
	case r.mu <- struct{}{}:
		defer func() { <-r.mu }()
		return len(r.got)
	default:
		return -1
	}
}

// waitFor ждёт выполнения условия (фоновые отправки асинхронны).
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("условие не выполнено за %s", d)
}

// newTestServer поднимает сервер админки на SQLite в памяти с пройденным мастером
// установки и одним root-админом (пароль "password").
func newTestServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	// Уникальная БД в памяти на каждый тест (cache=shared не изолирует тесты).
	cfg := config.Config{DBType: config.DBSQLite,
		DBPath: "file:mailtest" + strconv.FormatInt(time.Now().UnixNano(), 36) + "?mode=memory&cache=shared"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	st, err := store.Open(context.Background(), cfg, nopMigrationLogger{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hash, err := auth.HashPassword("password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.CreateRootAdmin(context.Background(), "root", hash, ""); err != nil {
		t.Fatalf("create root: %v", err)
	}
	srv.SetStore(st)
	return srv, st
}

// do выполняет запрос через обработчик сервера.
func do(t *testing.T, srv *Server, method, target string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
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
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestSaveMailSettingsRequiresAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	// Без входа — редирект на /login.
	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{"host": {"x"}}, nil)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/login") {
		t.Fatalf("expected redirect to login, got %d %q", w.Code, w.Header().Get("Location"))
	}
	// С root-админом — сохранение проходит.
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	w = do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"465"}, "security": {"smtps"},
		"username": {"mailer"}, "password": {"secret"},
		"from_addr": {"lib@example.com"}, "enabled": {"1"},
	}, hdr)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "flash=saved") {
		t.Fatalf("expected flash=saved, got %q", loc)
	}
}

func TestSaveMailSettingsValidation(t *testing.T) {
	srv, st := newTestServer(t)
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	// Пустой хост — ошибка в редиректе, настройки не применены.
	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {""}, "port": {"587"}, "security": {"starttls"}, "from_addr": {"lib@example.com"},
	}, hdr)
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "err=host") {
		t.Fatalf("expected err=host, got %q", loc)
	}
	got, _ := st.GetMailSettings(context.Background())
	if got.Host != "" || got.Enabled {
		t.Fatalf("settings must not be saved: %+v", got)
	}
	// Пароль без шифрования — ошибка.
	w = do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"25"}, "security": {"none"},
		"username": {"u"}, "password": {"p"}, "from_addr": {"lib@example.com"},
	}, hdr)
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "err=nocrypt") {
		t.Fatalf("expected err=nocrypt, got %q", loc)
	}
}

func TestEditMailKeepsPassword(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	if err := st.SaveMailSettings(ctx, store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		Username: "u", Password: "old-secret", FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	// Пустое поле пароля — пароль сохраняется.
	do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"username": {"u"}, "password": {""}, "from_addr": {"lib2@example.com"},
	}, hdr)
	got, _ := st.GetMailSettings(ctx)
	if got.Password != "old-secret" {
		t.Fatalf("password must be kept, got %q", got.Password)
	}
	if got.FromAddr != "lib2@example.com" || got.Enabled {
		t.Fatalf("unexpected settings: %+v", got)
	}
	// «-» очищает пароль.
	w := do(t, srv, http.MethodPost, "/settings/mail", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"username": {"u"}, "password": {"-"}, "from_addr": {"lib2@example.com"},
	}, hdr)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	got, _ = st.GetMailSettings(ctx)
	if got.Password != "" {
		t.Fatalf("password must be cleared, got %q", got.Password)
	}
}

func TestTestMailRejectsBadAddress(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	rec := newRecordingSender()
	srv.mail.setSender(rec)
	if err := st.SaveMailSettings(ctx, store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	w := do(t, srv, http.MethodPost, "/settings/mail/test",
		url.Values{"to": {"not-an-address"}}, hdr)
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "err=mail.check.syntax") {
		t.Fatalf("expected syntax error flash, got %q", loc)
	}
	// Письмо не отправлено.
	list, _ := st.ListMailSent(ctx, 10)
	if len(list) != 0 {
		t.Fatalf("no mail must be queued, got %d", len(list))
	}
}

// separateVerifyForm — отдельная <form ... action="/settings/mail/verify">
// (не formaction у кнопки основной формы).
var separateVerifyForm = regexp.MustCompile(`(?s)<form[^>]*action="/settings/mail/verify"`)

func TestMailSectionRenders(t *testing.T) {
	srv, _ := newTestServer(t)
	hdr := map[string]string{"Authorization": "Basic " + basicAuth("root", "password")}
	w := do(t, srv, http.MethodGet, "/settings?sec=mail", nil, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`name="host"`, `name="from_addr"`, `action="/settings/mail"`,
		`formaction="/settings/mail/verify"`, `action="/settings/mail/test"`,
		`action="/settings/mail/clear"`} {
		if !strings.Contains(body, want) {
			t.Errorf("страница «Почта» не содержит %q", want)
		}
	}
	// Регрессия: «Проверить соединение» отдельной формой отправляла пустой хост
	// (поля формы SMTP в чужую форму не попадают) — пользователь видел
	// «Укажите SMTP-сервер (хост)» при заполненном поле. Кнопка проверки
	// должна быть частью формы /settings/mail (formaction).
	if separateVerifyForm.MatchString(body) {
		t.Error("отдельная форма проверки: поля SMTP не передаются, хост придёт пустым")
	}
	// Поле отправителя — text, а не type=email: браузер не должен блокировать
	// ввод адресов, которые наш сервер принимает (валидация на бэкенде).
	if strings.Contains(body, `name="from_addr" type="email"`) {
		t.Error("from_addr должен быть type=text: валидацию делает сервер")
	}
	// Пароль не попадает в HTML.
	if strings.Contains(body, "secret") {
		t.Error("пароль не должен попадать в HTML")
	}
}

func TestPasswordResetFlow(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	rec := newRecordingSender()
	srv.mail.setSender(rec)
	// Стабильный детерминированный резолвер: любой домен имеет A-запись.
	srv.mailCheck = newStubChecker(t)

	hash, _ := auth.HashPassword("password")
	uid := createUserWithEmail(t, st, "reader", "reader@example.com", hash)
	if err := st.SaveMailSettings(ctx, store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("mail settings: %v", err)
	}
	// Тест делает несколько запросов сброса подряд — лимитеры не должны мешать.
	srv.resetByEmail.Reset()
	srv.resetByIP.Reset()

	// 1. Запрос сброса: письмо ставится в отправку.
	w := do(t, srv, http.MethodPost, "/reset", url.Values{"email": {"reader@example.com"}}, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("reset submit: %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "sent=1") {
		t.Fatalf("expected sent=1, got %q", loc)
	}
	waitFor(t, 2*time.Second, func() bool { _, ok := rec.last(); return ok })
	msg, _ := rec.last()
	if msg.Template != mail.TemplatePasswordReset || msg.To != "reader@example.com" || msg.UserID != uid {
		t.Fatalf("unexpected message: %+v", msg)
	}
	// Ссылка содержит токен.
	const marker = "/reset/confirm?token="
	idx := strings.Index(msg.Text, marker)
	if idx < 0 {
		t.Fatalf("в письме нет ссылки: %s", msg.Text)
	}
	token := msg.Text[idx+len(marker):]
	if i := strings.IndexAny(token, "\r\n <"); i >= 0 {
		token = token[:i]
	}

	// 2. Неверный токен — форма с ошибкой.
	w = do(t, srv, http.MethodGet, "/reset/confirm?token=bogus", nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad token: expected 400, got %d", w.Code)
	}

	// 3. Форма нового пароля по верной ссылке.
	w = do(t, srv, http.MethodGet, "/reset/confirm?token="+url.QueryEscape(token), nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="password"`) {
		t.Fatalf("confirm form: %d", w.Code)
	}

	// 4. Слишком короткий пароль отклоняется.
	w = do(t, srv, http.MethodPost, "/reset/confirm", url.Values{
		"token": {token}, "password": {"short"}, "password2": {"short"},
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("short password: expected 400, got %d", w.Code)
	}

	// 5. Успешная смена пароля.
	w = do(t, srv, http.MethodPost, "/reset/confirm", url.Values{
		"token": {token}, "password": {"new-password-1"}, "password2": {"new-password-1"},
	}, nil)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "/login") {
		t.Fatalf("reset confirm: %d %q", w.Code, w.Header().Get("Location"))
	}
	u, err := st.GetUserByID(ctx, uid)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	ok, err := auth.VerifyPassword("new-password-1", u.PasswordHash)
	if err != nil || !ok {
		t.Fatalf("новый пароль не сохранён: %v", err)
	}

	// 6. Токен одноразовый: повторная попытка не проходит.
	w = do(t, srv, http.MethodGet, "/reset/confirm?token="+url.QueryEscape(token), nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("token must be one-time, got %d", w.Code)
	}
}

func TestResetUnknownEmailSilent(t *testing.T) {
	srv, st := newTestServer(t)
	rec := newRecordingSender()
	srv.mail.setSender(rec)
	srv.mailCheck = newStubChecker(t)
	if err := st.SaveMailSettings(context.Background(), store.MailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls",
		FromAddr: "lib@example.com",
	}, 0); err != nil {
		t.Fatalf("mail settings: %v", err)
	}
	w := do(t, srv, http.MethodPost, "/reset", url.Values{"email": {"nobody@example.com"}}, nil)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "sent=1") {
		t.Fatalf("ожидался такой же ответ, получено %d %q", w.Code, w.Header().Get("Location"))
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := rec.last(); ok {
		t.Fatal("письмо на неизвестный адрес отправлено быть не должно")
	}
}

func TestResetDisabledWhenMailOff(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(t, srv, http.MethodPost, "/reset", url.Values{"email": {"reader@example.com"}}, nil)
	// Без настроек почты — просто редирект назад, письма нет.
	if w.Code != http.StatusSeeOther {
		t.Fatalf("unexpected status %d", w.Code)
	}
	// На странице /reset видно сообщение о недоступности.
	w = do(t, srv, http.MethodGet, "/reset", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `name="email"`) {
		t.Fatal("форма запроса не должна показываться при выключенной почте")
	}
}

// basicAuth кодирует учётные данные для HTTP Basic.
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// createUserWithEmail создаёт обычного читателя с почтой (для сценария сброса).
func createUserWithEmail(t *testing.T, st store.Store, username, email, hash string) int64 {
	t.Helper()
	id, err := st.(*store.DBStore).CreateUser(context.Background(), username, email, hash, "reader")
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return id
}

// nopMigrationLogger — «тихий» логгер миграций для тестов.
type nopMigrationLogger struct{}

func (nopMigrationLogger) Infof(string, string, ...any)  {}
func (nopMigrationLogger) Debugf(string, string, ...any) {}

// newStubChecker — чекер адресов без обращения к DNS: все корректные по
// синтаксису адреса считаются пригодными (тесты не зависят от сети).
func newStubChecker(t *testing.T) *emailcheck.Checker {
	t.Helper()
	return emailcheck.NewCheckerWith(emailcheck.ResolverFunc(func(ctx context.Context, host string) ([]string, error) {
		return []string{"192.0.2.1"}, nil
	}))
}
