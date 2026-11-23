package store

import (
	"context"
	"fmt"
)

// ListUsers возвращает всех пользователей (для настроек админки).
func (s *DBStore) ListUsers(ctx context.Context) ([]User, error) {
	q := fmt.Sprintf("SELECT %s FROM users ORDER BY id", s.userSelectCols())
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list users rows: %w", err)
	}
	return users, nil
}
