package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
)

// IsInstalled сообщает, создан ли root-админ (мастер установки пройден).
func (s *DBStore) IsInstalled(ctx context.Context) (bool, error) {
	var n int64
	q := "SELECT COUNT(1) FROM users WHERE is_root = " + s.boolTrue()
	if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("store: is installed: %w", err)
	}
	return n > 0, nil
}

// boolTrue возвращает литерал «истина» для диалекта.
func (s *DBStore) boolTrue() string {
	if s.kind == config.DBSQLite {
		return "1"
	}
	return "TRUE"
}

// CreateRootAdmin создаёт единственного root-администратора.
// Если root уже существует — возвращает ErrRootExists.
func (s *DBStore) CreateRootAdmin(ctx context.Context, username, passwordHash, email string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("store: empty username")
	}
	if passwordHash == "" {
		return 0, errors.New("store: empty password hash")
	}

	var emailArg any
	if e := strings.TrimSpace(email); e != "" {
		emailArg = e
	}

	q := fmt.Sprintf(
		"INSERT INTO users (username, email, password_hash, role, is_root, is_active) "+
			"VALUES (%s, %s, %s, 'admin', %s, %s) RETURNING id",
		s.ph(1), s.ph(2), s.ph(3), s.boolTrue(), s.boolTrue(),
	)

	var id int64
	err := s.db.QueryRowContext(ctx, q, username, emailArg, passwordHash).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrRootExists
		}
		return 0, fmt.Errorf("store: create root admin: %w", err)
	}
	return id, nil
}

// CreateUser создаёт обычного пользователя (не root) и возвращает id.
// Роль по умолчанию — reader; is_active всегда выставлен.
func (s *DBStore) CreateUser(ctx context.Context, username, email, passwordHash, role string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("store: empty username")
	}
	if passwordHash == "" {
		return 0, errors.New("store: empty password hash")
	}
	if role == "" {
		role = "reader"
	}
	var emailArg any
	if e := strings.TrimSpace(email); e != "" {
		emailArg = e
	}
	q := fmt.Sprintf("INSERT INTO users (username, email, password_hash, role, is_root, is_active) "+
		"VALUES (%s, %s, %s, %s, FALSE, TRUE) RETURNING id",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4))
	var id int64
	if err := s.db.QueryRowContext(ctx, q, username, emailArg, passwordHash, role).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return 0, ErrRootExists
		}
		return 0, fmt.Errorf("store: create user: %w", err)
	}
	return id, nil
}

// GetUserByUsername ищет пользователя по логину.
func (s *DBStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	q := fmt.Sprintf("SELECT %s FROM users WHERE username = %s", s.userSelectCols(), s.ph(1))
	u, err := scanUser(s.db.QueryRowContext(ctx, q, username))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get user: %w", err)
	}
	return u, nil
}

// GetUserByID возвращает пользователя по id (ErrNotFound, если нет).
func (s *DBStore) GetUserByID(ctx context.Context, id int64) (*User, error) {
	q := fmt.Sprintf("SELECT %s FROM users WHERE id = %s", s.userSelectCols(), s.ph(1))
	u, err := scanUser(s.db.QueryRowContext(ctx, q, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get user by id: %w", err)
	}
	return u, nil
}

// GetUserByEmail ищет пользователя по адресу почты (без учёта регистра).
func (s *DBStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, ErrNotFound
	}
	q := fmt.Sprintf("SELECT %s FROM users WHERE LOWER(email) = LOWER(%s) ORDER BY id LIMIT 1",
		s.userSelectCols(), s.ph(1))
	u, err := scanUser(s.db.QueryRowContext(ctx, q, email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get user by email: %w", err)
	}
	return u, nil
}

// asBool приводит значение (PG bool или SQLite 0/1) к bool.
func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case []byte:
		return len(x) > 0 && (x[0] == '1' || x[0] == 't' || x[0] == 'T')
	case string:
		return x == "1" || x == "true" || x == "t"
	default:
		return false
	}
}

// SetSetting сохраняет настройку (upsert).
func (s *DBStore) SetSetting(ctx context.Context, key, value string, updatedBy int64) error {
	var byArg any
	if updatedBy > 0 {
		byArg = updatedBy
	}

	// UPSERT: PG — ON CONFLICT, SQLite — ON CONFLICT (синтаксис близок).
	q := fmt.Sprintf(
		"INSERT INTO settings (key, value, updated_by_id) VALUES (%s, %s, %s) "+
			"ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, "+
			"updated_at = CURRENT_TIMESTAMP, updated_by_id = EXCLUDED.updated_by_id",
		s.ph(1), s.ph(2), s.ph(3))

	if _, err := s.db.ExecContext(ctx, q, key, value, byArg); err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}

// GetSetting читает настройку.
func (s *DBStore) GetSetting(ctx context.Context, key string) (string, error) {
	q := fmt.Sprintf("SELECT value FROM settings WHERE key = %s", s.ph(1))
	var v string
	if err := s.db.QueryRowContext(ctx, q, key).Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("store: get setting %q: %w", key, err)
	}
	return v, nil
}

// createdAtExpr возвращает выражение, приводящее created_at к строке ISO-8601.
// col — имя колонки (для JOIN — с префиксом, например "u.created_at").
func (s *DBStore) createdAtExpr(col string) string {
	if col == "" {
		col = "created_at"
	}
	if s.kind == config.DBSQLite {
		return col // уже TEXT ISO-8601
	}
	return "to_char(" + col + " AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')"
}

// isUniqueViolation определяет нарушение уникальности в PG/SQLite по строке.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "constraint failed")
}

// SetUserPasswordHash обновляет хеш пароля пользователя по id.
func (s *DBStore) SetUserPasswordHash(ctx context.Context, id int64, passwordHash string) error {
	if passwordHash == "" {
		return errors.New("store: empty password hash")
	}
	q := fmt.Sprintf("UPDATE users SET password_hash = %s WHERE id = %s", s.ph(1), s.ph(2))
	if _, err := s.db.ExecContext(ctx, q, passwordHash, id); err != nil {
		return fmt.Errorf("store: set password hash: %w", err)
	}
	return nil
}
