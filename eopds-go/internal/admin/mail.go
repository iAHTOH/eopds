package admin

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/mail"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// mailService — отправка писем портала: берёт настройки из БД, ставит запись
// в журнал, отправляет в отдельной горутине и обновляет её итог.
//
// Очереди в БД нет: каждое письмо пишется в mail_sent со статусом queued и
// затем обновляется до sent/error. После перезапуска процесса записи в
// статусе queued остаются как след незавершённой отправки — ретраи не делаются
// (для наших сценариев — сброс пароля и тесты — это безопасно).
type mailService struct {
	srv *Server
	mu  sync.RWMutex
	snd mail.Sender
	// timeout — предельное время одной отправки.
	timeout time.Duration
}

// newMailService создаёт сервис отправки с реальным SMTP-клиентом.
func newMailService(srv *Server) *mailService {
	return &mailService{srv: srv, snd: mail.NewClient(), timeout: 30 * time.Second}
}

// setSender подменяет отправителя (для тестов).
func (m *mailService) setSender(s mail.Sender) {
	m.mu.Lock()
	m.snd = s
	m.mu.Unlock()
}

// sender возвращает активного отправителя.
func (m *mailService) sender() mail.Sender {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snd
}

// loadSettings читает настройки SMTP из БД (с дефолтами).
func (m *mailService) loadSettings(ctx context.Context, st store.Store) (mail.Settings, error) {
	raw, err := st.GetMailSettings(ctx)
	if err != nil {
		return mail.Settings{}, err
	}
	return toMailSettings(raw), nil
}

// toMailSettings преобразует запись БД в настройки клиента.
func toMailSettings(s store.MailSettings) mail.Settings {
	return mail.Settings{
		Enabled:    s.Enabled,
		Host:       s.Host,
		Port:       s.Port,
		Security:   s.Security,
		Username:   s.Username,
		Password:   s.Password,
		FromAddr:   s.FromAddr,
		FromName:   s.FromName,
		ReplyTo:    s.ReplyTo,
		VerifiedAt: s.VerifiedAt,
		VerifyMsg:  s.VerifyMsg,
	}
}

// sendAsync отправляет письмо в фоне и пишет журнал.
// Возвращает id записи журнала (0 — журнал недоступен).
func (m *mailService) sendAsync(settings mail.Settings, msg mail.Message) int64 {
	st := m.srv.getStore()
	if st == nil {
		return 0
	}
	ctx := context.WithoutCancel(context.Background())
	id, err := st.AddMailSent(ctx, store.MailSent{
		Recipient: msg.To,
		Subject:   msg.Subject,
		Template:  msg.Template,
		Status:    mail.StatusQueued,
		Lang:      msg.Lang,
		UserID:    msg.UserID,
	})
	if err != nil {
		m.srv.log.Warn("mail: journal", "err", err)
	}
	go m.deliver(id, settings, msg)
	return id
}

// deliver выполняет отправку и обновляет запись журнала.
func (m *mailService) deliver(id int64, settings mail.Settings, msg mail.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	res := m.sender().Send(ctx, settings, msg)
	status, errText := mail.StatusSent, ""
	if res.Err != nil {
		status, errText = mail.StatusError, res.Err.Error()
		m.srv.log.Warn("mail: send failed", "to", maskEmail(msg.To), "template", msg.Template, "err", res.Err)
	} else {
		m.srv.log.Info("mail: sent", "to", maskEmail(msg.To), "template", msg.Template)
	}

	st := m.srv.getStore()
	if st == nil || id <= 0 {
		return
	}
	if err := st.UpdateMailSent(ctx, id, status, errText, res.MessageID); err != nil {
		m.srv.log.Warn("mail: journal update", "err", err)
	}

	// Операционное событие: успех — info, ошибка — warn (категория mail.sent).
	args := map[string]string{"to": maskEmail(msg.To), "template": msg.Template}
	if res.Err != nil {
		args["error"] = trimText(res.Err.Error(), 200)
		m.srv.logEvent(ctx, store.EventWarn, "mail.error", args)
		return
	}
	m.srv.logEvent(ctx, store.EventInfo, "mail.sent", args)
}

// maskEmail скрывает локальную часть адреса для логов: "iv***@mail.ru".
func maskEmail(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 {
		return "***"
	}
	local := addr[:at]
	if len(local) > 2 {
		local = local[:2]
	}
	return local + "***" + addr[at:]
}
