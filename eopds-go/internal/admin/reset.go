package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/emailcheck"
	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Срок жизни ссылки сброса пароля и минимальная длина нового пароля.
const (
	resetTTL         = time.Hour
	resetMinPassword = 8
)

// Ограничение частоты запросов сброса пароля (защита от генерации писем).
// Значения подобраны под типовые лимиты провайдеров SMTP (например, Gmail
// не даёт отправить более ~500 писем в сутки с одного ящика).
const (
	resetPerEmailLimit  = 3         // запросов на адрес…
	resetPerEmailWindow = time.Hour // …за час
	resetPerIPLimit     = 10        // запросов с одного IP…
	resetPerIPWindow    = time.Hour // …за час
)

// newResetToken генерирует случайный токен (32 байта) и его SHA-256 (hex).
// В БД попадает только хеш; сам токен уходит в письме.
func newResetToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

// hashToken возвращает SHA-256 токена в hex.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// settingMailPublicURL — ключ настройки публичного адреса портала
// (для ссылок в письмах). Редактируется в разделе «Почта (SMTP)».
const settingMailPublicURL = "mail.public_url"

// normalizePublicURL приводит введённый публичный адрес к каноничному виду
// «схема://хост[:порт][/путь]» без завершающего «/».
// Возвращает ошибку, если адрес пустой (после TrimSpace), без схемы
// http/https, без хоста или содержит пробелы.
func normalizePublicURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errPublicURLEmpty
	}
	// Пробелы внутри адреса — почти всегда след Copy/Paste с переносами.
	if strings.ContainsAny(v, " \t\r\n") {
		return "", errPublicURLBad
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", errPublicURLBad
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errPublicURLBad
	}
	if u.Host == "" {
		return "", errPublicURLBad
	}
	// Query и fragment в базовом адресе не имеют смысла — считаем ошибкой,
	// чтобы не собрать потом битую ссылку.
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errPublicURLBad
	}
	u.Scheme = scheme
	return strings.TrimRight(u.String(), "/"), nil
}

var (
	errPublicURLEmpty = errors.New("mail public url: empty")
	errPublicURLBad   = errors.New("mail public url: bad")
)

// mailPublicURL читает сохранённый публичный адрес портала (” — не задан).
func (s *Server) mailPublicURL(ctx context.Context, st store.Store) string {
	if st == nil {
		return ""
	}
	v, err := st.GetSetting(ctx, settingMailPublicURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// resetBaseURL строит базовый адрес портала для ссылок в письмах.
// Публичный адрес можно задать настройкой mail.public_url (поле в разделе
// «Почта (SMTP)»); иначе используется схема и хост текущего запроса.
func (s *Server) resetBaseURL(r *http.Request) string {
	if v := s.mailPublicURL(r.Context(), s.getStore()); v != "" {
		if v = strings.TrimRight(v, "/"); v != "" {
			return v
		}
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

// mailReady сообщает, настроена ли отправка (для показа ссылки «Забыли пароль?»).
func (s *Server) mailReady(ctx context.Context) bool {
	st := s.getStore()
	if st == nil {
		return false
	}
	raw, err := st.GetMailSettings(ctx)
	if err != nil || !raw.Enabled {
		return false
	}
	return toMailSettings(raw).Configured()
}

// handleResetForm показывает форму запроса сброса пароля (GET /reset).
func (s *Server) handleResetForm(w http.ResponseWriter, r *http.Request) {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	data := s.baseData(r)
	data["Title"] = "reset"
	data["MailReady"] = s.mailReady(r.Context())
	if !data["MailReady"].(bool) {
		data["Error"] = s.translatorFor(r)("reset.disabled")
	}
	if msg := r.URL.Query().Get("sent"); msg == "1" {
		data["Info"] = s.translatorFor(r)("reset.sent")
	}
	s.render(w, r, "reset", data)
}

// handleResetSubmit принимает адрес и отправляет письмо со ссылкой
// (POST /reset). Ответ всегда одинаковый — чтобы не раскрывать, какие
// адреса зарегистрированы.
func (s *Server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	tr := s.translatorFor(r)
	_ = r.ParseForm()
	email := strings.TrimSpace(r.PostFormValue("email"))
	back := "/reset?lang=" + resolveLang(r)

	if !s.mailReady(r.Context()) {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if !emailcheck.ValidSyntax(email) {
		data := s.baseData(r)
		data["Title"] = "reset"
		data["MailReady"] = true
		data["Error"] = tr("reset.invalid_email")
		data["Email"] = email
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "reset", data)
		return
	}

	// Ограничение частоты: проверяем ДО обращения к DNS и постановки письма
	// в очередь. Ответ — всё та же форма «письмо отправлено», чтобы не
	// раскрывать факт срабатывания лимита; отдельно пишем событие.
	if !s.allowReset(r, email) {
		http.Redirect(w, r, back+"&sent=1", http.StatusSeeOther)
		return
	}

	// Адрес должен существовать (MX/A): на недоставляемые адреса не пишем.
	chk := s.mailCheck.Check(r.Context(), email)
	if chk.Valid() {
		s.startPasswordReset(r, email)
	} else {
		s.log.Info("сброс пароля: адрес не проходит проверку", "status", chk.Status)
	}
	http.Redirect(w, r, back+"&sent=1", http.StatusSeeOther)
}

// allowReset проверяет лимит запросов сброса по адресу и по IP.
// При превышении фиксирует событие mail.reset_rate_limited и возвращает false.
func (s *Server) allowReset(r *http.Request, email string) bool {
	okEmail, waitEmail := s.resetByEmail.Allow(email)
	okIP, waitIP := s.resetByIP.Allow(clientIP(r))
	if okEmail && okIP {
		return true
	}
	wait := waitEmail
	if waitIP > wait {
		wait = waitIP
	}
	s.log.Warn("сброс пароля: превышен лимит запросов",
		"by_email", !okEmail, "by_ip", !okIP)
	s.logEvent(r.Context(), store.EventWarn, "mail.reset_rate_limited", map[string]string{
		"email":       maskEmail(email),
		"by_email":    onOff(!okEmail),
		"by_ip":       onOff(!okIP),
		"retry_after": retryAfterHeader(wait),
	})
	return false
}

// startPasswordReset создаёт токен и ставит письмо в отправку.
// Не выполняется, если адрес не принадлежит ни одному пользователю или
// пользователь неактивен.
func (s *Server) startPasswordReset(r *http.Request, email string) {
	st := s.getStore()
	if st == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	u, err := st.GetUserByEmail(ctx, email)
	if err != nil || u == nil || !u.IsActive {
		return
	}
	settings, err := st.GetMailSettings(ctx)
	if err != nil || !settings.Enabled {
		return
	}
	token, hash, err := newResetToken()
	if err != nil {
		s.log.Error("сброс пароля: токен", "err", err)
		return
	}
	// Новый запрос отменяет предыдущие ссылки того же вида.
	_ = st.DeleteEmailTokens(ctx, u.ID, store.TokenResetPassword)
	now := time.Now().UTC()
	if _, err := st.CreateEmailToken(ctx, store.EmailToken{
		UserID:    u.ID,
		Email:     email,
		Kind:      store.TokenResetPassword,
		TokenHash: hash,
		TS:        now.UnixMilli(),
		ExpiresAt: now.Add(resetTTL).UnixMilli(),
	}); err != nil {
		s.log.Error("сброс пароля: сохранение токена", "err", err)
		return
	}

	link := s.resetBaseURL(r) + "/reset/confirm?token=" + url.QueryEscape(token)
	lang := mail.Lang(u.PrefLang)
	ttlText := "1 час"
	if lang == "en" {
		ttlText = "1 hour"
	}
	letter := mail.PasswordReset(lang, "EOPDS", u.Username, link, ttlText)
	s.mail.sendAsync(toMailSettings(settings), mail.Message{
		To:       email,
		ToName:   u.Username,
		Subject:  letter.Subject,
		Text:     letter.Text,
		HTML:     letter.HTML,
		Template: mail.TemplatePasswordReset,
		Lang:     lang,
		UserID:   u.ID,
	})
	s.logEvent(ctx, store.EventInfo, "mail.password_reset_requested", map[string]string{
		"email": maskEmail(email),
	})
}

// handleResetConfirm показывает форму нового пароля или применяет его
// (GET/POST /reset/confirm?token=...).
func (s *Server) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	tr := s.translatorFor(r)
	token := r.URL.Query().Get("token")
	if token == "" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		token = r.PostFormValue("token")
	}
	renderErr := func(msg string) {
		data := s.baseData(r)
		data["Title"] = "reset"
		data["MailReady"] = true
		data["Error"] = msg
		data["Token"] = token
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "reset_confirm", data)
	}
	renderForm := func(displayEmail string) {
		data := s.baseData(r)
		data["Title"] = "reset"
		data["MailReady"] = true
		data["Token"] = token
		data["Email"] = displayEmail
		s.render(w, r, "reset_confirm", data)
	}

	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/reset", http.StatusSeeOther)
		return
	}
	tok, err := st.GetEmailToken(r.Context(), hashToken(token), store.TokenResetPassword)
	if err != nil {
		renderErr(tr("reset.bad_token"))
		return
	}

	if r.Method == http.MethodGet {
		renderForm(tok.Email)
		return
	}

	// POST: проверяем и сохраняем новый пароль.
	_ = r.ParseForm()
	pw1 := r.PostFormValue("password")
	pw2 := r.PostFormValue("password2")
	if len([]rune(pw1)) < resetMinPassword {
		renderErr(tr("reset.password_short"))
		return
	}
	if pw1 != pw2 {
		renderErr(tr("reset.password_mismatch"))
		return
	}
	u, err := st.GetUserByID(r.Context(), tok.UserID)
	if err != nil || u == nil || !u.IsActive {
		renderErr(tr("reset.bad_token"))
		return
	}
	hash, err := auth.HashPassword(pw1)
	if err != nil {
		s.log.Error("сброс пароля: хеш", "err", err)
		renderErr(tr("login.err.internal"))
		return
	}
	if err := st.SetUserPasswordHash(r.Context(), u.ID, hash); err != nil {
		s.log.Error("сброс пароля: сохранение", "err", err)
		renderErr(tr("login.err.internal"))
		return
	}
	// Токен одноразовый; все сессии пользователя отзываются.
	_ = st.UseEmailToken(r.Context(), tok.ID)
	_, _ = st.DeleteUserSessionsExcept(r.Context(), u.ID, "")

	s.log.Info("пароль сброшен по почте", "user", u.Username)
	s.logEvent(r.Context(), store.EventWarn, "mail.password_reset_done", map[string]string{
		"email": maskEmail(tok.Email),
	})
	http.Redirect(w, r, "/login?reset=1&lang="+resolveLang(r), http.StatusSeeOther)
}
