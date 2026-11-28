package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// handleSaveMail сохраняет настройки SMTP (POST /settings/mail).
//
// Пустое поле пароля означает «не менять»; значение «-» очищает пароль.
// При изменении параметров соединения отметка проверки сбрасывается.
func (s *Server) handleSaveMail(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Error(w, "no store", http.StatusServiceUnavailable)
		return
	}
	prev, err := st.GetMailSettings(r.Context())
	if err != nil {
		s.log.Warn("settings: mail settings", "err", err)
	}
	next, code := parseMailSettings(r, prev)
	if code != "" {
		s.mailFlashRedirect(w, r, "", code, "", "smtp")
		return
	}

	var uid int64
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	if err := st.SaveMailSettings(r.Context(), next, uid); err != nil {
		s.log.Error("settings: save mail", "err", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	// Публичный адрес портала — отдельная настройка, но сохраняется вместе
	// с SMTP: от него зависят ссылки в письмах. Пустое поле — «не задан»
	// (тогда базой станет адрес текущего запроса).
	if err := s.saveMailPublicURL(r.Context(), st, r.PostFormValue("public_url"), uid); err != nil {
		switch {
		case errors.Is(err, errPublicURLEmpty), errors.Is(err, errPublicURLBad):
			// Ошибка валидации публичного адреса — обрабатывается ниже.
		default:
			s.log.Error("settings: save mail public url", "err", err)
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		// Настройки SMTP уже сохранены, но публичный адрес — нет: возвращаем
		// в форму с ошибкой и введённым адресом (параметр pu), чтобы
		// администратор исправил его, не набирая заново.
		s.mailURLFlashRedirect(w, r, "public_url", strings.TrimSpace(r.PostFormValue("public_url")))
		return
	}
	s.logEvent(r.Context(), store.EventInfo, "mail.settings_saved", map[string]string{
		"host":     next.Host,
		"port":     itoa(next.Port),
		"security": next.Security,
		"enabled":  onOff(next.Enabled),
	})
	s.log.Info("настройки почты сохранены", "host", next.Host, "port", next.Port,
		"security", next.Security, "enabled", next.Enabled)
	s.mailFlashRedirect(w, r, "saved", "", "", "smtp")
}

// saveMailPublicURL проверяет и сохраняет публичный адрес портала.
// Пустая строка означает «не задан» — настройка очищается.
func (s *Server) saveMailPublicURL(ctx context.Context, st store.Store, raw string, uid int64) error {
	if strings.TrimSpace(raw) == "" {
		return st.SetSetting(ctx, settingMailPublicURL, "", uid)
	}
	v, err := normalizePublicURL(raw)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, settingMailPublicURL, v, uid)
}

// mailURLFlashRedirect возвращает в раздел «Почта» с ошибкой публичного
// адреса; параметр pu переносит введённое значение обратно в форму
// (после редиректа тела POST уже нет).
func (s *Server) mailURLFlashRedirect(w http.ResponseWriter, r *http.Request, errCode, value string) {
	q := url.Values{}
	q.Set("sec", "mail")
	q.Set("lang", resolveLang(r))
	q.Set("err", errCode)
	if value != "" {
		q.Set("pu", value)
	}
	http.Redirect(w, r, "/settings?"+q.Encode()+"#smtp", http.StatusSeeOther)
}

// handleVerifyMail проверяет соединение с SMTP с учётом сохранённого пароля
// (POST /settings/mail/verify). Введённые в форме значения применяются
// «на лету», но не сохраняются — для сохранения есть отдельная кнопка.
func (s *Server) handleVerifyMail(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Error(w, "no store", http.StatusServiceUnavailable)
		return
	}
	prev, err := st.GetMailSettings(r.Context())
	if err != nil {
		s.log.Warn("settings: mail settings", "err", err)
	}
	cand, code := parseMailSettings(r, prev)
	if code != "" {
		s.mailFlashRedirect(w, r, "", code, "", "smtp")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	settings := toMailSettings(cand)
	detail := describeConnection(settings)
	verr := s.mailClient().Verify(ctx, settings)

	// Запоминаем результат проверки (времени + пояснения) в сохранённых настройках.
	prev.VerifiedAt = 0
	prev.VerifyMsg = ""
	if verr == nil {
		prev.VerifiedAt = time.Now().UnixMilli()
		prev.VerifyMsg = detail
	} else {
		prev.VerifyMsg = trimText(verr.Error(), 300)
	}
	var uid int64
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	if err := st.SaveMailSettings(r.Context(), prev, uid); err != nil {
		s.log.Warn("settings: save verify result", "err", err)
	}

	if verr != nil {
		s.log.Warn("почта: проверка соединения не удалась", "host", cand.Host, "err", verr)
		s.logEvent(r.Context(), store.EventWarn, "mail.verify_failed", map[string]string{
			"host":  cand.Host,
			"error": trimText(verr.Error(), 200),
		})
		s.mailFlashRedirect(w, r, "verify_failed", "", "", "smtp")
		return
	}
	s.log.Info("почта: проверка соединения успешна", "host", cand.Host, "port", cand.Port)
	s.logEvent(r.Context(), store.EventInfo, "mail.verify_ok", map[string]string{
		"host":     cand.Host,
		"port":     itoa(cand.Port),
		"security": cand.Security,
	})
	s.mailFlashRedirect(w, r, "verified", "", "", "smtp")
}

// handleTestMail отправляет тестовое письмо на указанный адрес
// (POST /settings/mail/test). Адрес проверяется (синтаксис + MX) —
// на заведомо недоставляемые адреса письмо не отправляется.
func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Error(w, "no store", http.StatusServiceUnavailable)
		return
	}
	_ = r.ParseForm()
	to := strings.TrimSpace(r.PostFormValue("to"))
	prev, err := st.GetMailSettings(r.Context())
	if err != nil {
		s.log.Warn("settings: mail settings", "err", err)
	}
	settings := toMailSettings(prev)
	if !settings.Configured() {
		s.mailFlashRedirect(w, r, "", "incomplete", to, "smtp")
		return
	}
	if !settings.Enabled {
		s.mailFlashRedirect(w, r, "", "disabled", to, "smtp")
		return
	}
	if to == "" {
		s.mailFlashRedirect(w, r, "", "to_empty", "", "smtp")
		return
	}
	// Валидация адреса: синтаксис + существование домена.
	res := s.mailCheck.Check(r.Context(), to)
	if !res.Valid() {
		s.mailFlashRedirect(w, r, "", res.ReasonKey(), to, "smtp")
		return
	}

	lang := resolveLang(r)
	letter := mail.Test(lang, "EOPDS", describeConnection(settings))
	msg := mail.Message{
		To:       to,
		Subject:  letter.Subject,
		Text:     letter.Text,
		HTML:     letter.HTML,
		Template: mail.TemplateTest,
		Lang:     lang,
	}
	s.mail.sendAsync(settings, msg)
	s.mailFlashRedirect(w, r, "test_queued", "", to, "smtp")
}

// handleClearMailLog очищает журнал отправок (POST /settings/mail/clear).
func (s *Server) handleClearMailLog(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Error(w, "no store", http.StatusServiceUnavailable)
		return
	}
	if err := st.ClearMailSent(r.Context()); err != nil {
		s.log.Error("settings: clear mail log", "err", err)
		http.Error(w, "clear failed", http.StatusInternalServerError)
		return
	}
	s.logEvent(r.Context(), store.EventWarn, "mail.log_cleared", nil)
	s.mailFlashRedirect(w, r, "log_cleared", "", "", "log")
}

// mailClient возвращает SMTP-клиент сервиса (тип Sender как *mail.Client).
func (s *Server) mailClient() *mail.Client {
	if c, ok := s.mail.sender().(*mail.Client); ok {
		return c
	}
	return mail.NewClient()
}

// describeConnection — краткое описание соединения для письма/подтверждения.
func describeConnection(s mail.Settings) string {
	mode := map[string]string{
		mail.SecuritySMTPS:    "SMTPS (TLS)",
		mail.SecuritySTARTTLS: "STARTTLS",
		mail.SecurityNone:     "без шифрования",
	}[s.Security]
	if mode == "" {
		mode = s.Security
	}
	out := s.Host + ":" + itoa(s.Port) + ", " + mode
	if s.Username != "" {
		out += ", аутентификация: " + s.Username
	}
	return out
}

// onOff — "on"/"off" для подстановки в события.
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
