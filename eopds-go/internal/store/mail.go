package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
)

// MailSettings — настройки исходящей почты (таблица mail_smtp, одна строка).
//
// Пароль хранится как есть: SMTP-AUTH требует исходный секрет, обратимого
// способа хранить его нет. В интерфейс пароль не отдаётся.
type MailSettings struct {
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
	UpdatedAt  string // RFC3339 ('' — не сохранялось)
}

// MailSent — запись журнала отправок (таблица mail_sent).
type MailSent struct {
	ID        int64
	TS        int64 // unix ms
	Recipient string
	Subject   string
	Template  string
	Status    string // queued | sent | error
	Error     string
	MessageID string
	Lang      string
	UserID    int64 // 0 — нет
}

// Виды одноразовых токенов (email_tokens.kind).
const (
	TokenResetPassword = "reset_password"
	TokenVerifyEmail   = "verify_email"
)

// EmailToken — одноразовый токен из письма (хранится только хеш).
type EmailToken struct {
	ID        int64
	UserID    int64
	Email     string
	Kind      string
	TokenHash string
	TS        int64 // unix ms создания
	ExpiresAt int64 // unix ms истечения
	UsedAt    int64 // unix ms использования (0 — не использован)
}

// GetMailSettings возвращает настройки SMTP. Если строка ещё не создана,
// возвращает значения по умолчанию (без ошибки).
func (s *DBStore) GetMailSettings(ctx context.Context) (MailSettings, error) {
	q := fmt.Sprintf("SELECT enabled, host, port, security, username, password, "+
		"from_addr, from_name, reply_to, verified_at, verify_message, updated_at "+
		"FROM mail_smtp WHERE id = %s", s.ph(1))
	var (
		m       MailSettings
		enabled any
	)
	err := s.db.QueryRowContext(ctx, q, 1).Scan(&enabled, &m.Host, &m.Port, &m.Security,
		&m.Username, &m.Password, &m.FromAddr, &m.FromName, &m.ReplyTo,
		&m.VerifiedAt, &m.VerifyMsg, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MailSettings{Port: 587, Security: "starttls"}, nil
	}
	if err != nil {
		return MailSettings{}, fmt.Errorf("store: get mail settings: %w", err)
	}
	m.Enabled = asBool(enabled)
	return m, nil
}

// SaveMailSettings сохраняет настройки SMTP (upsert по id = 1).
func (s *DBStore) SaveMailSettings(ctx context.Context, m MailSettings, updatedBy int64) error {
	var uid any
	if updatedBy > 0 {
		uid = updatedBy
	}
	enabled := "FALSE"
	if m.Enabled {
		enabled = "TRUE"
	}
	if s.kind == config.DBSQLite {
		enabled = "0"
		if m.Enabled {
			enabled = "1"
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := fmt.Sprintf("INSERT INTO mail_smtp (id, enabled, host, port, security, username, "+
		"password, from_addr, from_name, reply_to, verified_at, verify_message, "+
		"updated_at, updated_by_id) VALUES (1, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s) "+
		"ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, host = EXCLUDED.host, "+
		"port = EXCLUDED.port, security = EXCLUDED.security, username = EXCLUDED.username, "+
		"password = EXCLUDED.password, from_addr = EXCLUDED.from_addr, "+
		"from_name = EXCLUDED.from_name, reply_to = EXCLUDED.reply_to, "+
		"verified_at = EXCLUDED.verified_at, verify_message = EXCLUDED.verify_message, "+
		"updated_at = EXCLUDED.updated_at, updated_by_id = EXCLUDED.updated_by_id",
		enabled, s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8),
		s.ph(9), s.ph(10), s.ph(11), s.ph(12))
	args := []any{strings.TrimSpace(m.Host), m.Port, m.Security, m.Username, m.Password,
		strings.TrimSpace(m.FromAddr), m.FromName, strings.TrimSpace(m.ReplyTo),
		m.VerifiedAt, m.VerifyMsg, now, uid}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: save mail settings: %w", err)
	}
	return nil
}

// AddMailSent добавляет запись журнала отправок, возвращает её id.
func (s *DBStore) AddMailSent(ctx context.Context, m MailSent) (int64, error) {
	if m.TS == 0 {
		m.TS = time.Now().UTC().UnixMilli()
	}
	if m.Status == "" {
		m.Status = "queued"
	}
	var uid any
	if m.UserID > 0 {
		uid = m.UserID
	}
	cols := "ts, recipient, subject, template, status, error, message_id, lang, user_id"
	vals := fmt.Sprintf("%s, %s, %s, %s, %s, %s, %s, %s, %s",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9))
	args := []any{m.TS, m.Recipient, m.Subject, m.Template, m.Status, m.Error,
		m.MessageID, m.Lang, uid}

	q := fmt.Sprintf("INSERT INTO mail_sent (%s) VALUES (%s)", cols, vals)
	if s.kind != config.DBSQLite {
		q += " RETURNING id"
		var id int64
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
			return 0, fmt.Errorf("store: add mail sent: %w", err)
		}
		return id, nil
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: add mail sent: %w", err)
	}
	return res.LastInsertId()
}

// UpdateMailSent меняет статус записи журнала.
func (s *DBStore) UpdateMailSent(ctx context.Context, id int64, status, errMsg, messageID string) error {
	q := fmt.Sprintf("UPDATE mail_sent SET status = %s, error = %s, message_id = %s WHERE id = %s",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4))
	if _, err := s.db.ExecContext(ctx, q, status, errMsg, messageID, id); err != nil {
		return fmt.Errorf("store: update mail sent: %w", err)
	}
	return nil
}

// ListMailSent возвращает последние limit записей журнала (новые сверху).
func (s *DBStore) ListMailSent(ctx context.Context, limit int) ([]MailSent, error) {
	if limit <= 0 {
		limit = 100
	}
	q := fmt.Sprintf("SELECT id, ts, recipient, subject, template, status, error, "+
		"message_id, lang, COALESCE(user_id, 0) FROM mail_sent "+
		"ORDER BY ts DESC, id DESC LIMIT %s", s.ph(1))
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list mail sent: %w", err)
	}
	defer rows.Close()

	var out []MailSent
	for rows.Next() {
		var m MailSent
		if err := rows.Scan(&m.ID, &m.TS, &m.Recipient, &m.Subject, &m.Template,
			&m.Status, &m.Error, &m.MessageID, &m.Lang, &m.UserID); err != nil {
			return nil, fmt.Errorf("store: scan mail sent: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClearMailSent очищает журнал отправок.
func (s *DBStore) ClearMailSent(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM mail_sent"); err != nil {
		return fmt.Errorf("store: clear mail sent: %w", err)
	}
	return nil
}

// CreateEmailToken сохраняет токен (только хеш), возвращает id.
func (s *DBStore) CreateEmailToken(ctx context.Context, t EmailToken) (int64, error) {
	if t.TS == 0 {
		t.TS = time.Now().UTC().UnixMilli()
	}
	q := fmt.Sprintf("INSERT INTO email_tokens (user_id, email, kind, token_hash, ts, "+
		"expires_at, used_at) VALUES (%s, %s, %s, %s, %s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7))
	args := []any{t.UserID, t.Email, t.Kind, t.TokenHash, t.TS, t.ExpiresAt, t.UsedAt}
	if s.kind != config.DBSQLite {
		var id int64
		if err := s.db.QueryRowContext(ctx, q+" RETURNING id", args...).Scan(&id); err != nil {
			return 0, fmt.Errorf("store: create email token: %w", err)
		}
		return id, nil
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: create email token: %w", err)
	}
	return res.LastInsertId()
}

// GetEmailToken ищет действующий (не истёкший и не использованный) токен по хешу.
func (s *DBStore) GetEmailToken(ctx context.Context, tokenHash, kind string) (EmailToken, error) {
	now := time.Now().UTC().UnixMilli()
	q := fmt.Sprintf("SELECT id, user_id, email, kind, token_hash, ts, expires_at, used_at "+
		"FROM email_tokens WHERE token_hash = %s AND kind = %s AND used_at = 0 "+
		"AND expires_at > %s", s.ph(1), s.ph(2), s.ph(3))
	var t EmailToken
	err := s.db.QueryRowContext(ctx, q, tokenHash, kind, now).Scan(&t.ID, &t.UserID, &t.Email,
		&t.Kind, &t.TokenHash, &t.TS, &t.ExpiresAt, &t.UsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailToken{}, ErrNotFound
	}
	if err != nil {
		return EmailToken{}, fmt.Errorf("store: get email token: %w", err)
	}
	return t, nil
}

// UseEmailToken помечает токен использованным.
func (s *DBStore) UseEmailToken(ctx context.Context, id int64) error {
	q := fmt.Sprintf("UPDATE email_tokens SET used_at = %s WHERE id = %s AND used_at = 0",
		s.ph(1), s.ph(2))
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC().UnixMilli(), id); err != nil {
		return fmt.Errorf("store: use email token: %w", err)
	}
	return nil
}

// DeleteEmailTokens удаляет все токены пользователя указанного вида.
func (s *DBStore) DeleteEmailTokens(ctx context.Context, userID int64, kind string) error {
	q := fmt.Sprintf("DELETE FROM email_tokens WHERE user_id = %s AND kind = %s",
		s.ph(1), s.ph(2))
	if _, err := s.db.ExecContext(ctx, q, userID, kind); err != nil {
		return fmt.Errorf("store: delete email tokens: %w", err)
	}
	return nil
}

// PurgeEmailTokens удаляет истёкшие и использованные токены, возвращая
// число удалённых строк (для записи в журнал обслуживания).
func (s *DBStore) PurgeEmailTokens(ctx context.Context) (int64, error) {
	now := time.Now().UTC().UnixMilli()
	q := fmt.Sprintf("DELETE FROM email_tokens WHERE expires_at <= %s OR used_at > 0", s.ph(1))
	res, err := s.db.ExecContext(ctx, q, now)
	if err != nil {
		return 0, fmt.Errorf("store: purge email tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// Драйвер не сообщает число строк — это не ошибка очистки.
		return 0, nil
	}
	return n, nil
}
