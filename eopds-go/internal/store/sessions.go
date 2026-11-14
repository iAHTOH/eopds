package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateSession сохраняет сессию пользователя.
func (s *DBStore) CreateSession(ctx context.Context, id string, userID int64, expiresAt time.Time) error {
	q := fmt.Sprintf(
		"INSERT INTO sessions (id, user_id, expires_at) VALUES (%s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3))
	if _, err := s.db.ExecContext(ctx, q, id, userID, expiresAt.UTC()); err != nil {
		return fmt.Errorf("store: create session: %w", err)
	}
	return nil
}

// GetSessionUser возвращает пользователя активной (неистёкшей) сессии.
// ErrNotFound, если сессии нет, истекла или пользователь неактивен.
func (s *DBStore) GetSessionUser(ctx context.Context, id string) (*User, error) {
	bt := s.boolTrue()
	q := fmt.Sprintf(
		"SELECT u.id, u.username, u.email, u.password_hash, u.role, u.is_root, u.is_active, %s "+
			"FROM sessions s JOIN users u ON u.id = s.user_id "+
			"WHERE s.id = %s AND s.expires_at > %s AND u.is_active = %s",
		s.createdAtExpr("u.created_at"), s.ph(1), s.ph(2), bt)

	u := &User{}
	var email sql.NullString
	var isRoot, isActive any
	err := s.db.QueryRowContext(ctx, q, id, time.Now().UTC()).Scan(
		&u.ID, &u.Username, &email, &u.PasswordHash, &u.Role,
		&isRoot, &isActive, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get session user: %w", err)
	}
	u.IsRoot = asBool(isRoot)
	u.IsActive = asBool(isActive)
	if email.Valid {
		u.Email = &email.String
	}
	return u, nil
}

// DeleteSession удаляет сессию (logout).
func (s *DBStore) DeleteSession(ctx context.Context, id string) error {
	q := fmt.Sprintf("DELETE FROM sessions WHERE id = %s", s.ph(1))
	if _, err := s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// PurgeExpiredSessions удаляет истёкшие сессии.
func (s *DBStore) PurgeExpiredSessions(ctx context.Context) error {
	q := fmt.Sprintf("DELETE FROM sessions WHERE expires_at <= %s", s.ph(1))
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC()); err != nil {
		return fmt.Errorf("store: purge sessions: %w", err)
	}
	return nil
}
