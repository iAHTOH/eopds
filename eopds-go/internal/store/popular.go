package store

import (
	"context"
	"fmt"
)

// PopularKind — критерий популярности.
const (
	PopularByDownloads = "downloads"
	PopularByRating    = "rating"
)

// bookCols — общий список колонок книги для выборок (совпадает с LibraryBook).
const bookCols = "b.id, b.title, b.format, b.size_bytes, b.lang, b.docdate, b.annotation, b.path, b.filename"

// BookDownloadCount возвращает число «читателей» книги: уникальные
// пользователи, скачавшие/прочитавшие её, плюс анонимные события
// (user_id IS NULL). С повторами не растёт (вариант A).
func (s *DBStore) BookDownloadCount(ctx context.Context, bookID int64) (int64, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(DISTINCT user_id) + "+
			"COALESCE(SUM(CASE WHEN user_id IS NULL THEN 1 ELSE 0 END), 0) "+
			"FROM downloads WHERE book_id = %s", s.ph(1))
	var n int64
	if err := s.db.QueryRowContext(ctx, q, bookID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: download count: %w", err)
	}
	return n, nil
}

// PopularBooks возвращает постранично книги, отсортированные по популярности.
// kind: PopularByDownloads — по числу скачиваний, PopularByRating — по средней
// оценке (затем по числу оценок); в рейтинге — только книги с оценками.
// asc: false — по убыванию (по умолчанию), true — по возрастанию.
func (s *DBStore) PopularBooks(ctx context.Context, kind string, page, size int, asc bool) (LibraryPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	offset := (page - 1) * size
	dir := "DESC"
	if asc {
		dir = "ASC"
	}

	var (
		countQ string
		listQ  string
	)
	switch kind {
	case PopularByRating:
		// Только книги с оценками; сортировка по среднему, затем числу оценок.
		countQ = "SELECT COUNT(*) FROM (SELECT book_id FROM book_ratings GROUP BY book_id) r"
		sub := "SELECT book_id, AVG(score) AS avg_score, COUNT(*) AS cnt FROM book_ratings GROUP BY book_id"
		listQ = "SELECT " + bookCols + " FROM books b JOIN (" + sub + ") r ON r.book_id = b.id " +
			" ORDER BY r.avg_score " + dir + ", r.cnt " + dir + ", b.search_title, b.id" +
			" LIMIT " + s.ph(1) + " OFFSET " + s.ph(2)
	default: // PopularByDownloads
		// Популярность = число уникальных пользователей + анонимные события.
		countQ = "SELECT COUNT(*) FROM (SELECT book_id FROM downloads GROUP BY book_id) d"
		sub := "SELECT book_id, " +
			"COUNT(DISTINCT user_id) + COALESCE(SUM(CASE WHEN user_id IS NULL THEN 1 ELSE 0 END), 0) AS cnt " +
			"FROM downloads GROUP BY book_id"
		listQ = "SELECT " + bookCols + " FROM books b JOIN (" + sub + ") d ON d.book_id = b.id " +
			" ORDER BY d.cnt " + dir + ", b.search_title, b.id" +
			" LIMIT " + s.ph(1) + " OFFSET " + s.ph(2)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("store: popular count: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, listQ, size, offset)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("store: popular list: %w", err)
	}
	defer rows.Close()

	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
			&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
			return LibraryPage{}, fmt.Errorf("store: popular scan: %w", err)
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, err
	}
	if err := s.fillBookRelations(ctx, items); err != nil {
		return LibraryPage{}, err
	}
	pages := int((total + int64(size) - 1) / int64(size))
	return LibraryPage{Items: items, Total: total, Page: page, Size: size, Pages: pages}, nil
}
