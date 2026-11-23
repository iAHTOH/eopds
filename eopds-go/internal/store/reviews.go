package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// RatingSummary — сводка оценок книги.
type RatingSummary struct {
	// Count — число оценок.
	Count int64
	// Avg — средняя оценка в звёздах (0..5), с дробной частью.
	Avg float64
	// MyScore — оценка текущего пользователя (0..10, 0 — не оценивал).
	// Заполняется только если задан userID > 0.
	MyScore int
}

// BookComment — отзыв пользователя к книге.
type BookComment struct {
	ID         int64
	BookID     int64
	UserID     int64
	UserName   string // имя для отображения (DisplayName или Username)
	UserAvatar string // имя файла аватара ("" — нет)
	Body       string
	CreatedAt  string // ISO-8601 (RFC3339), одинаково для PG/SQLite
	// CanDelete — может ли текущий пользователь удалить отзыв.
	CanDelete bool
}

// SetBookRating сохраняет/обновляет оценку пользователя (score 0..10).
// Если пользователь уже оценивал книгу — обновляет значение.
func (s *DBStore) SetBookRating(ctx context.Context, bookID, userID int64, score int) error {
	if bookID <= 0 || userID <= 0 {
		return fmt.Errorf("store: set rating: bad ids")
	}
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}
	updated := "CURRENT_TIMESTAMP"
	if s.dialectIsPG() {
		updated = "now()"
	}
	q := fmt.Sprintf(
		"INSERT INTO book_ratings (book_id, user_id, score) VALUES (%s, %s, %s) "+
			"ON CONFLICT (book_id, user_id) DO UPDATE SET score = excluded.score, updated_at = %s",
		s.ph(1), s.ph(2), s.ph(3), updated)
	if _, err := s.db.ExecContext(ctx, q, bookID, userID, score); err != nil {
		return fmt.Errorf("store: set rating: %w", err)
	}
	return nil
}

// BookRatingSummary возвращает агрегат (число оценок, среднее в звёздах) и,
// если userID > 0, оценку этого пользователя.
func (s *DBStore) BookRatingSummary(ctx context.Context, bookID, userID int64) (RatingSummary, error) {
	var res RatingSummary
	var avg sql.NullFloat64
	q := fmt.Sprintf("SELECT COUNT(*), AVG(score) FROM book_ratings WHERE book_id = %s", s.ph(1))
	if err := s.db.QueryRowContext(ctx, q, bookID).Scan(&res.Count, &avg); err != nil {
		return RatingSummary{}, fmt.Errorf("store: rating summary: %w", err)
	}
	if avg.Valid {
		// score 0..10 → звёзды 0..5.
		res.Avg = avg.Float64 / 2.0
	}
	if userID > 0 && res.Count > 0 {
		var score int
		mq := fmt.Sprintf("SELECT score FROM book_ratings WHERE book_id = %s AND user_id = %s", s.ph(1), s.ph(2))
		err := s.db.QueryRowContext(ctx, mq, bookID, userID).Scan(&score)
		if err == nil {
			res.MyScore = score
		} else if err != sql.ErrNoRows {
			return RatingSummary{}, fmt.Errorf("store: my rating: %w", err)
		}
	}
	return res, nil
}

// AddBookComment добавляет отзыв и возвращает его id.
func (s *DBStore) AddBookComment(ctx context.Context, bookID, userID int64, body string) (int64, error) {
	body = strings.TrimSpace(body)
	if bookID <= 0 || userID <= 0 || body == "" {
		return 0, fmt.Errorf("store: add comment: bad args")
	}
	if s.dialectIsPG() {
		var id int64
		q := fmt.Sprintf(
			"INSERT INTO book_comments (book_id, user_id, body) VALUES (%s, %s, %s) RETURNING id",
			s.ph(1), s.ph(2), s.ph(3))
		if err := s.db.QueryRowContext(ctx, q, bookID, userID, body).Scan(&id); err != nil {
			return 0, fmt.Errorf("store: add comment: %w", err)
		}
		return id, nil
	}
	q := fmt.Sprintf("INSERT INTO book_comments (book_id, user_id, body) VALUES (%s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3))
	res, err := s.db.ExecContext(ctx, q, bookID, userID, body)
	if err != nil {
		return 0, fmt.Errorf("store: add comment: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// BookComments возвращает отзывы к книге (новые сверху), не более limit.
// viewerID — текущий пользователь (для флага CanDelete); viewerIsModerator —
// может ли удалять чужие отзывы (модератор/админ).
func (s *DBStore) BookComments(ctx context.Context, bookID int64, limit int, viewerID int64, viewerIsModerator bool) ([]BookComment, error) {
	if limit <= 0 {
		limit = 50
	}
	q := "SELECT c.id, c.book_id, c.user_id, " +
		"COALESCE(NULLIF(u.display_name, ''), u.username) AS uname, " +
		"COALESCE(u.avatar, ''), c.body, " + s.createdAtExpr("c.created_at") + " " +
		"FROM book_comments c JOIN users u ON u.id = c.user_id " +
		"WHERE c.book_id = " + s.ph(1) + " ORDER BY c.created_at DESC, c.id DESC LIMIT " + s.ph(2)
	rows, err := s.db.QueryContext(ctx, q, bookID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: book comments: %w", err)
	}
	defer rows.Close()

	var out []BookComment
	for rows.Next() {
		var c BookComment
		if err := rows.Scan(&c.ID, &c.BookID, &c.UserID, &c.UserName, &c.UserAvatar, &c.Body, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan comment: %w", err)
		}
		c.CanDelete = viewerIsModerator || (viewerID > 0 && c.UserID == viewerID)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteBookComment удаляет отзыв по id. Если byModerator == false, удалить
// можно только свой отзыв (проверяется владелец). Возвращает ErrNotFound, если
// отзыв не найден или не принадлежит пользователю без прав модератора.
func (s *DBStore) DeleteBookComment(ctx context.Context, commentID, userID int64, byModerator bool) error {
	if commentID <= 0 || userID <= 0 {
		return ErrNotFound
	}
	var owner int64
	q := fmt.Sprintf("SELECT user_id FROM book_comments WHERE id = %s", s.ph(1))
	if err := s.db.QueryRowContext(ctx, q, commentID).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("store: delete comment owner: %w", err)
	}
	if !byModerator && owner != userID {
		return ErrNotFound
	}
	dq := fmt.Sprintf("DELETE FROM book_comments WHERE id = %s", s.ph(1))
	if _, err := s.db.ExecContext(ctx, dq, commentID); err != nil {
		return fmt.Errorf("store: delete comment: %w", err)
	}
	return nil
}
