package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/emailcheck"
	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// mailLogLimit — сколько записей журнала отправок показывать в разделе.
const mailLogLimit = 50

// mailView — данные формы SMTP для шаблона.
// Пароль НИКОГДА не отдаётся в HTML: только флаг «задан».
type mailView struct {
	Enabled bool
	// Host и др. — сохранённые значения.
	Host         string
	Port         int
	Security     string
	Username     string
	PasswordSet  bool
	FromAddr     string
	FromName     string
	ReplyTo      string
	VerifiedAt   string // локальное время успешной проверки ('' — не проверялось)
	VerifyMsg    string
	UpdatedAt    string
	Configured   bool // заполнены обязательные поля
	SenderStatus string

	// Значения, которые имели смысл в последнем POST — подставляются в форму,
	// чтобы при ошибке валидации не терять введённое (и чтобы повторная
	// «Проверка соединения» работала без ручного повторного ввода).
	FormHost     string
	FormPort     int
	FormSecurity string
	FormUsername string
	FormFromAddr string
	FormFromName string
	FormReplyTo  string
	FormEnabled  bool
}

// mailSentView — строка журнала отправок.
type mailSentView struct {
	Time      string
	Recipient string
	Subject   string
	Template  string
	Status    string // queued | sent | error
	Error     string
}

// mailData наполняет данные шаблона разделом «Почта (SMTP)».
func (s *Server) mailData(r *http.Request, data map[string]any, st store.Store) {
	raw, err := st.GetMailSettings(r.Context())
	if err != nil {
		s.log.Warn("settings: mail settings", "err", err)
	}
	loc := time.Local
	v := mailView{
		Enabled:     raw.Enabled,
		Host:        raw.Host,
		Port:        raw.Port,
		Security:    mail.NormalizeSecurity(raw.Security),
		Username:    raw.Username,
		PasswordSet: raw.Password != "",
		FromAddr:    raw.FromAddr,
		FromName:    raw.FromName,
		ReplyTo:     raw.ReplyTo,
		VerifyMsg:   raw.VerifyMsg,
	}
	// По умолчанию в форме — сохранённые значения.
	v.FormHost, v.FormPort, v.FormSecurity = v.Host, v.Port, v.Security
	v.FormUsername, v.FormFromAddr, v.FormFromName, v.FormReplyTo = v.Username, v.FromAddr, v.FromName, v.ReplyTo
	v.FormEnabled = v.Enabled
	// Ошибка/проверка пришли POST-ом — показываем введённое, а не сохранённое.
	if r.Method == http.MethodPost {
		if f, _ := parseMailForm(r); r.PostFormValue("host") != "" || r.PostFormValue("from_addr") != "" {
			v.FormHost, v.FormPort, v.FormSecurity = f.Host, f.Port, f.Security
			v.FormUsername, v.FormFromAddr = f.Username, f.FromAddr
			v.FormFromName, v.FormReplyTo = f.FromName, f.ReplyTo
			v.FormEnabled = f.Enabled
			if f.Password != "" && f.Password != "-" {
				v.PasswordSet = true
			}
		}
	}
	settings := toMailSettings(raw)
	v.Configured = settings.Configured()
	if raw.VerifiedAt > 0 {
		v.VerifiedAt = time.UnixMilli(raw.VerifiedAt).In(loc).Format("02.01.2006 15:04:05")
	}
	if raw.UpdatedAt != "" {
		if t, perr := time.Parse(time.RFC3339, raw.UpdatedAt); perr == nil {
			v.UpdatedAt = t.In(loc).Format("02.01.2006 15:04:05")
		} else {
			v.UpdatedAt = raw.UpdatedAt
		}
	}
	switch {
	case !raw.Enabled:
		v.SenderStatus = "off"
	case settings.Configured():
		v.SenderStatus = "on"
	default:
		v.SenderStatus = "incomplete"
	}
	data["SMTP"] = v

	// Журнал отправок (новые сверху).
	sent, err := st.ListMailSent(r.Context(), mailLogLimit)
	if err != nil {
		s.log.Warn("settings: mail log", "err", err)
	}
	views := make([]mailSentView, 0, len(sent))
	for _, m := range sent {
		views = append(views, mailSentView{
			Time:      time.UnixMilli(m.TS).In(loc).Format("02.01.2006 15:04:05"),
			Recipient: m.Recipient,
			Subject:   m.Subject,
			Template:  m.Template,
			Status:    m.Status,
			Error:     trimText(m.Error, 300),
		})
	}
	data["MailSent"] = views
	data["MailLogLimit"] = mailLogLimit

	if fl := r.URL.Query().Get("flash"); fl != "" {
		data["MailFlash"] = fl
	}
	if er := r.URL.Query().Get("err"); er != "" {
		data["MailError"] = mailErrorText(r, er)
	}
	if ad := r.URL.Query().Get("test_to"); ad != "" {
		data["TestTo"] = ad
	} else if raw.FromAddr != "" {
		data["TestTo"] = raw.FromAddr
	}
}

// mailFlashRedirect формирует редирект обратно в раздел «Почта» с сообщением.
func (s *Server) mailFlashRedirect(w http.ResponseWriter, r *http.Request, flash, errCode, testTo string, anchor string) {
	q := url.Values{}
	q.Set("sec", "mail")
	q.Set("lang", resolveLang(r))
	if flash != "" {
		q.Set("flash", flash)
	}
	if errCode != "" {
		q.Set("err", errCode)
	}
	if testTo != "" {
		q.Set("test_to", testTo)
	}
	target := "/settings?" + q.Encode()
	if anchor != "" {
		target += "#" + anchor
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// parseMailSettings разбирает форму SMTP.
// keepPassword: пустое поле пароля означает «не менять» (prev — текущий).
func parseMailSettings(r *http.Request, prev store.MailSettings) (store.MailSettings, string) {
	m, _ := parseMailForm(r)
	m.Password = prev.Password
	// Пароль: пустая строка — оставляем прежний; «-» — очистить.
	if pw := r.PostFormValue("password"); pw != "" {
		if pw == "-" {
			m.Password = ""
		} else {
			m.Password = pw
		}
	}
	// Настройки изменились — подтверждение соединения сбрасывается.
	if m.Host != prev.Host || m.Port != prev.Port || m.Security != prev.Security ||
		m.Username != prev.Username || m.Password != prev.Password {
		m.VerifiedAt = 0
		m.VerifyMsg = ""
	} else {
		m.VerifiedAt = prev.VerifiedAt
		m.VerifyMsg = prev.VerifyMsg
	}
	if err := toMailSettings(m).Validate(); err != nil {
		return m, mailErrorCode(err)
	}
	return m, ""
}

// parseMailForm читает поля формы SMTP «как есть» (без подстановки прежнего
// пароля). Используется и для сохранения, и для повторного показа формы.
func parseMailForm(r *http.Request) (store.MailSettings, bool) {
	_ = r.ParseForm()
	m := store.MailSettings{
		Enabled:  r.PostFormValue("enabled") != "",
		Host:     strings.TrimSpace(r.PostFormValue("host")),
		Security: mail.NormalizeSecurity(r.PostFormValue("security")),
		Username: strings.TrimSpace(r.PostFormValue("username")),
		FromAddr: strings.TrimSpace(r.PostFormValue("from_addr")),
		FromName: strings.TrimSpace(r.PostFormValue("from_name")),
		ReplyTo:  strings.TrimSpace(r.PostFormValue("reply_to")),
	}
	m.Port = mail.PortFromString(r.PostFormValue("port"), mail.DefaultPort(m.Security))
	if pw := r.PostFormValue("password"); pw != "-" {
		m.Password = pw
	}
	return m, true
}

// mailErrorCode переводит ошибку настроек в короткий код для URL.
func mailErrorCode(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "host is empty"):
		return "host"
	case strings.Contains(msg, "bad port"):
		return "port"
	case strings.Contains(msg, "from address is empty"):
		return "from_empty"
	case strings.Contains(msg, "bad from address"):
		return "from"
	case strings.Contains(msg, "bad reply-to"):
		return "reply_to"
	case strings.Contains(msg, "unencrypted"):
		return "nocrypt"
	case strings.Contains(msg, "security"):
		return "security"
	default:
		return "invalid"
	}
}

// mailErrorText — локализованный текст ошибки по коду.
func mailErrorText(r *http.Request, code string) string {
	tr := resolveLang(r)
	ru := []struct{ code, text string }{
		{"host", "Укажите SMTP-сервер (хост)."},
		{"port", "Некорректный порт (1–65535)."},
		{"security", "Некорректный режим шифрования."},
		{"from_empty", "Укажите адрес отправителя (From)."},
		{"from", "Некорректный адрес отправителя (From)."},
		{"reply_to", "Некорректный адрес Reply-To."},
		{"nocrypt", "Пароль нельзя использовать без шифрования: выберите SMTPS или STARTTLS."},
		{"invalid", "Настройки не сохранены: проверьте поля."},
	}
	en := []struct{ code, text string }{
		{"host", "SMTP host is required."},
		{"port", "Invalid port (1–65535)."},
		{"security", "Invalid encryption mode."},
		{"from_empty", "From address is required."},
		{"from", "Invalid From address."},
		{"reply_to", "Invalid Reply-To address."},
		{"nocrypt", "A password cannot be used without encryption: choose SMTPS or STARTTLS."},
		{"invalid", "Settings were not saved: please check the fields."},
	}
	list := ru
	if tr != "ru" {
		list = en
	}
	for _, it := range list {
		if it.code == code {
			return it.text
		}
	}
	return code
}

// checkAddresses проверяет адреса (синтаксис + DNS) для интерфейса.
func (s *Server) checkAddresses(ctx context.Context, addrs ...string) map[string]emailcheck.Result {
	out := make(map[string]emailcheck.Result, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		out[a] = s.mailCheck.Check(ctx, a)
	}
	return out
}
