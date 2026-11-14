package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListUsers возвращает всех пользователей (для настроек админки).
func (s *DBStore) ListUsers(ctx context.Context) ([]User, error) {
	q := fmt.Sprintf(
		"SELECT id, username, email, password_hash, role, is_root, is_active, %s "+
			"FROM users ORDER BY id", s.createdAtExpr(""))
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var email sql.NullString
		var isRoot, isActive any
		if err := rows.Scan(&u.ID, &u.Username, &email, &u.PasswordHash, &u.Role,
			&isRoot, &isActive, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		u.IsRoot = asBool(isRoot)
		u.IsActive = asBool(isActive)
		if email.Valid {
			u.Email = &email.String
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list users rows: %w", err)
	}
	return users, nil
}
