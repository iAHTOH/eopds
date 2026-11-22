package store

import (
	"context"
	"fmt"
)

// AddDownload фиксирует факт скачивания книги (таблица downloads).
// userID <= 0 — анонимное скачивание (downloads.user_id допускает NULL).
func (s *DBStore) AddDownload(ctx context.Context, bookID, userID int64, format string) error {
	q := fmt.Sprintf(
		"INSERT INTO downloads (book_id, user_id, format) VALUES (%s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3))
	var userArg any
	if userID > 0 {
		userArg = userID
	}
	if _, err := s.db.ExecContext(ctx, q, bookID, userArg, format); err != nil {
		return fmt.Errorf("store: add download: %w", err)
	}
	return nil
}
