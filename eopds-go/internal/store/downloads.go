package store

import (
	"context"
	"fmt"
)

// AddDownload фиксирует факт скачивания/чтения книги (таблица downloads).
// userID <= 0 — анонимное событие (user_id = NULL).
//
// Идемпотентно для залогиненных: одна запись на (book_id, user_id) — повторное
// скачивание/чтение/OPDS той же книги не создаёт дубль (уникальный индекс
// downloads_book_user_uidx, миграция 7; ON CONFLICT DO NOTHING).
func (s *DBStore) AddDownload(ctx context.Context, bookID, userID int64, format string) error {
	q := fmt.Sprintf(
		"INSERT INTO downloads (book_id, user_id, format) VALUES (%s, %s, %s) "+
			"ON CONFLICT DO NOTHING",
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
