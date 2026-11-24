// Package mail — отправка исходящей почты EOPDS (SMTP).
//
// Единственное место, знающее про SMTP-библиотеку. Всё остальное (админка,
// сценарии сброса пароля) работает через интерфейс Sender, что позволяет
// подменять отправку в тестах.
//
// Настройки SMTP задаются структурой Settings (они же хранятся в БД):
// host, port, security (smtps | starttls | none), username, password,
// адрес/имя отправителя и Reply-To.
//
// Режимы безопасности:
//   - smtps    — неявный TLS с первого байта (обычно порт 465);
//   - starttls — обычное соединение + обязательный STARTTLS (обычно 587);
//   - none     — без шифрования (порт 25; только для локального relay,
//     админ предупреждается в интерфейсе).
//
// Проверка соединения выполняет реальный диалог с сервером: TCP/TLS,
// EHLO и, если задан пользователь, AUTH — без отправки письма.
package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// Режимы безопасности соединения.
const (
	SecuritySMTPS    = "smtps"
	SecuritySTARTTLS = "starttls"
	SecurityNone     = "none"
)

// Статусы отправки (таблица mail_sent).
const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusError  = "error"
)

// Имена шаблонов писем (mail_sent.template).
const (
	TemplateTest          = "test"
	TemplatePasswordReset = "password_reset"
	TemplateVerifyEmail   = "verify_email"
)

// Ошибки пакета.
var (
	// ErrDisabled — отправка запрещена (SMTP выключен в настройках).
	ErrDisabled = errors.New("mail: SMTP is disabled")
	// ErrInvalidSettings — настройки неполны/некорректны.
	ErrInvalidSettings = errors.New("mail: invalid SMTP settings")
)

// Settings — настройки SMTP-сервера (соответствуют таблице mail_smtp).
type Settings struct {
	Enabled    bool
	Host       string
	Port       int
	Security   string // smtps | starttls | none
	Username   string
	Password   string
	FromAddr   string
	FromName   string
	ReplyTo    string
	VerifiedAt int64  // unix ms последней успешной проверки (0 — никогда)
	VerifyMsg  string // пояснение результата последней проверки
}

// Configured сообщает, заполнены ли обязательные поля (host, порт, From).
func (s Settings) Configured() bool {
	return strings.TrimSpace(s.Host) != "" && s.Port > 0 && s.Port <= 65535 &&
		strings.TrimSpace(s.FromAddr) != ""
}

// Validate проверяет настройки перед сохранением.
func (s Settings) Validate() error {
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("%w: host is empty", ErrInvalidSettings)
	}
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("%w: bad port", ErrInvalidSettings)
	}
	switch s.Security {
	case SecuritySMTPS, SecuritySTARTTLS, SecurityNone:
	default:
		return fmt.Errorf("%w: bad security %q", ErrInvalidSettings, s.Security)
	}
	if strings.TrimSpace(s.FromAddr) == "" {
		return fmt.Errorf("%w: from address is empty", ErrInvalidSettings)
	}
	if _, err := mail.ParseAddress(s.FromAddr); err != nil {
		return fmt.Errorf("%w: bad from address: %v", ErrInvalidSettings, err)
	}
	if rt := strings.TrimSpace(s.ReplyTo); rt != "" {
		if _, err := mail.ParseAddress(rt); err != nil {
			return fmt.Errorf("%w: bad reply-to: %v", ErrInvalidSettings, err)
		}
	}
	// «none» без шифрования с паролем — риск перехвата учётных данных.
	if s.Security == SecurityNone && s.Password != "" {
		return fmt.Errorf("%w: password over unencrypted connection", ErrInvalidSettings)
	}
	return nil
}

// Message — письмо к отправке.
type Message struct {
	To       string // адрес получателя
	ToName   string // имя получателя ('' — нет)
	Subject  string
	Text     string // текстовая часть
	HTML     string // HTML-часть ('' — только текст)
	Template string // имя шаблона (для журнала)
	Lang     string // язык письма (для журнала)
	UserID   int64  // получатель-пользователь (0 — нет; для журнала)
}

// Result — итог отправки.
type Result struct {
	MessageID string
	Err       error
}

// Sender — интерфейс отправки писем (подменяется в тестах).
type Sender interface {
	// Send отправляет письмо. Ошибка означает, что письмо НЕ отправлено.
	Send(ctx context.Context, settings Settings, m Message) Result
}

// Client — реализация Sender поверх go-mail.
type Client struct {
	// Timeout — общий таймаут соединения и операций (по умолчанию 20 с).
	Timeout time.Duration
}

// NewClient создаёт SMTP-клиент с таймаутом по умолчанию.
func NewClient() *Client { return &Client{Timeout: 20 * time.Second} }

// newClient собирает go-mail клиент из настроек.
func (c *Client) newClient(s Settings) (*gomail.Client, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	opts := []gomail.Option{
		gomail.WithPort(s.Port),
		gomail.WithTimeout(timeout),
		gomail.WithHELO(heloName()),
	}
	switch s.Security {
	case SecuritySMTPS:
		opts = append(opts, gomail.WithSSL(), gomail.WithTLSPolicy(gomail.TLSMandatory))
	case SecuritySTARTTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}
	if s.Username != "" {
		// Механизмы выбираем по порядку предпочтения: сначала «сильные»
		// (SCRAM, NTLM, CRAM-MD5), затем PLAIN/LOGIN. PLAIN и LOGIN допустимы
		// только под шифрованием — поэтому для соединения без TLS просим их
		// «-NOENC»-варианты, а go-mail откажется их использовать без SSL.
		prefer := []gomail.SMTPAuthType{
			gomail.SMTPAuthSCRAMSHA256PLUS, gomail.SMTPAuthSCRAMSHA256,
			gomail.SMTPAuthSCRAMSHA1PLUS, gomail.SMTPAuthSCRAMSHA1,
			gomail.SMTPAuthNTLM, gomail.SMTPAuthCramMD5,
		}
		if s.Security == SecurityNone {
			prefer = append(prefer, gomail.SMTPAuthPlainNoEnc, gomail.SMTPAuthLoginNoEnc)
		} else {
			prefer = append(prefer, gomail.SMTPAuthPlain, gomail.SMTPAuthLogin)
		}
		opts = append(opts, gomail.WithOpportunisticSMTPAuth(prefer...),
			gomail.WithUsername(s.Username), gomail.WithPassword(s.Password))
	}
	cl, err := gomail.NewClient(s.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: new client: %w", err)
	}
	return cl, nil
}

// heloName возвращает имя для EHLO (локальное имя хоста или "localhost").
func heloName() string {
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "localhost"
}

// Send отправляет письмо через SMTP.
func (c *Client) Send(ctx context.Context, s Settings, m Message) Result {
	if !s.Enabled {
		return Result{Err: ErrDisabled}
	}
	if err := s.Validate(); err != nil {
		return Result{Err: err}
	}
	if strings.TrimSpace(m.To) == "" {
		return Result{Err: errors.New("mail: empty recipient")}
	}

	msg, err := buildMsg(s, m)
	if err != nil {
		return Result{Err: err}
	}

	cl, err := c.newClient(s)
	if err != nil {
		return Result{Err: err}
	}
	// DialAndSendWithContext закрывает соединение за нас.
	if err := cl.DialAndSendWithContext(ctx, msg); err != nil {
		return Result{Err: fmt.Errorf("mail: send: %w", err)}
	}
	return Result{MessageID: msg.GetMessageID()}
}

// buildMsg собирает go-mail сообщение из письма и настроек.
func buildMsg(s Settings, m Message) (*gomail.Msg, error) {
	opts := []gomail.MsgOption{}
	msg := gomail.NewMsg(opts...)

	from := &mail.Address{Name: s.FromName, Address: s.FromAddr}
	if m.ToName != "" {
		msg.SetAddrHeaderFromMailAddress(gomail.HeaderTo, &mail.Address{Name: m.ToName, Address: m.To})
	} else if err := msg.SetAddrHeader(gomail.HeaderTo, m.To); err != nil {
		return nil, fmt.Errorf("mail: to: %w", err)
	}
	msg.SetAddrHeaderFromMailAddress(gomail.HeaderFrom, from)
	// Конверт (Envelope From) — всегда «голый» адрес отправителя.
	if err := msg.EnvelopeFrom(s.FromAddr); err != nil {
		return nil, fmt.Errorf("mail: envelope from: %w", err)
	}
	if rt := strings.TrimSpace(s.ReplyTo); rt != "" {
		_ = msg.SetAddrHeader(gomail.HeaderReplyTo, rt)
	}
	if m.Subject != "" {
		msg.Subject(m.Subject)
	}
	if m.HTML != "" {
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	} else {
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	}
	return msg, nil
}

// Verify выполняет реальную проверку соединения с SMTP-сервером без отправки
// письма: TCP/TLS, EHLO и AUTH (если задан пользователь). Возвращает nil при
// успехе. Настройка Enabled не требуется — проверка нужна и до включения.
func (c *Client) Verify(ctx context.Context, s Settings) error {
	if strings.TrimSpace(s.Host) == "" || s.Port <= 0 {
		return fmt.Errorf("%w: host/port required", ErrInvalidSettings)
	}
	switch s.Security {
	case SecuritySMTPS, SecuritySTARTTLS, SecurityNone:
	default:
		return fmt.Errorf("%w: bad security %q", ErrInvalidSettings, s.Security)
	}
	cl, err := c.newClient(s)
	if err != nil {
		return err
	}
	if err := cl.DialWithContext(ctx); err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	// Соединение установлено и аутентификация (если задана) пройдена.
	return cl.Close()
}

// DefaultPort возвращает порт по умолчанию для режима безопасности.
func DefaultPort(security string) int {
	switch security {
	case SecuritySMTPS:
		return 465
	case SecuritySTARTTLS:
		return 587
	default:
		return 25
	}
}

// NormalizeSecurity приводит значение режима к допустимому (дефолт starttls).
func NormalizeSecurity(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case SecuritySMTPS, "ssl", "tls", "465":
		return SecuritySMTPS
	case SecurityNone, "plain", "25":
		return SecurityNone
	default:
		return SecuritySTARTTLS
	}
}

// PortFromString парсит порт с дефолтом.
func PortFromString(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n <= 65535 {
		return n
	}
	return def
}
