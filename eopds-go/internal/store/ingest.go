package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// IngestBook — данные книги для импорта в БД (из записи сканера).
type IngestBook struct {
	CatalogType int
	RelPath     string
	BookPath    string
	Filename    string
	Format      string
	Size        int64
	Title       string
	Lang        string
	DocDate     string
	Annotation  string
	Authors     []string
	Genres      []string
	SeriesTitle string
	SeriesIndex int
}

// IngestResult — что произошло при импорте книги.
type IngestResult struct {
	BookID  int64
	Created bool // true — книга добавлена, false — обновлена существующая
}

// ImportBook вставляет/обновляет книгу и связанные сущности в одной транзакции.
// Уникальность книги — по (path, filename). Авторы — по full_name, серии — по title,
// жанры — по code. Повторный импорт той же книги обновляет запись (idempotent).
func (s *DBStore) ImportBook(ctx context.Context, b IngestBook) (IngestResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestResult{}, fmt.Errorf("store: import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	path := b.BookPath
	if path == "" {
		path = b.RelPath
	}
	title := strings.TrimSpace(b.Title)
	if title == "" {
		title = b.Filename
	}
	searchTitle := strings.ToLower(title)

	// Каталог книги (books.catalog_id NOT NULL): upsert по path.
	catalogID, err := s.ensureCatalog(ctx, tx, path)
	if err != nil {
		return IngestResult{}, err
	}

	// UPSERT книги по (path, filename).
	bookID, created, err := s.upsertBook(ctx, tx, catalogID, b, path, title, searchTitle)
	if err != nil {
		return IngestResult{}, err
	}

	// Авторы.
	for _, name := range b.Authors {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		authorID, err := s.selectOrInsert(ctx, tx,
			"INSERT INTO authors (full_name, search_full_name) VALUES (%s, %s) RETURNING id",
			"SELECT id FROM authors WHERE full_name = %s",
			name, name, strings.ToLower(name))
		if err != nil {
			return IngestResult{}, fmt.Errorf("store: upsert author: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_authors (book_id, author_id) VALUES (%s, %s)",
			bookID, authorID); err != nil {
			return IngestResult{}, err
		}
	}

	// Серия.
	if st := strings.TrimSpace(b.SeriesTitle); st != "" {
		seriesID, err := s.selectOrInsert(ctx, tx,
			"INSERT INTO series (title, search_title) VALUES (%s, %s) RETURNING id",
			"SELECT id FROM series WHERE title = %s",
			st, st, strings.ToLower(st))
		if err != nil {
			return IngestResult{}, fmt.Errorf("store: upsert series: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_series (book_id, series_id, seq_no) VALUES (%s, %s, %s)",
			bookID, seriesID, int64(b.SeriesIndex)); err != nil {
			return IngestResult{}, err
		}
	}

	// Жанры.
	for _, code := range b.Genres {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		genreID, err := s.selectOrInsert(ctx, tx,
			"INSERT INTO genres (code) VALUES (%s) RETURNING id",
			"SELECT id FROM genres WHERE code = %s",
			code, code)
		if err != nil {
			return IngestResult{}, fmt.Errorf("store: upsert genre: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_genres (book_id, genre_id) VALUES (%s, %s)",
			bookID, genreID); err != nil {
			return IngestResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return IngestResult{}, fmt.Errorf("store: commit book: %w", err)
	}
	return IngestResult{BookID: bookID, Created: created}, nil
}

// ensureCatalog возвращает id каталога по пути, создавая его при необходимости.
// name = последний элемент пути (или путь целиком для корня).
func (s *DBStore) ensureCatalog(ctx context.Context, tx *sql.Tx, path string) (int64, error) {
	name := path
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 && i+1 < len(path) {
		name = path[i+1:]
	}
	if name == "" {
		name = path
	}
	return s.selectOrInsert(ctx, tx,
		"INSERT INTO catalogs (name, path, kind) VALUES (%s, %s, 0) RETURNING id",
		"SELECT id FROM catalogs WHERE path = %s",
		path, name, path)
}

// upsertBook вставляет книгу или обновляет существующую по (path, filename).
func (s *DBStore) upsertBook(ctx context.Context, tx *sql.Tx, catalogID int64, b IngestBook, path, title, searchTitle string) (int64, bool, error) {
	// Проверяем существование.
	var existingID int64
	selQ := fmt.Sprintf("SELECT id FROM books WHERE path = %s AND filename = %s", s.ph(1), s.ph(2))
	err := tx.QueryRowContext(ctx, selQ, path, b.Filename).Scan(&existingID)
	switch {
	case err == nil:
		updQ := fmt.Sprintf(
			"UPDATE books SET title = %s, search_title = %s, format = %s, size_bytes = %s, "+
				"annotation = %s, docdate = %s, lang = %s, lang_code = %s, updated_at = CURRENT_TIMESTAMP "+
				"WHERE id = %s",
			s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9))
		if _, err := tx.ExecContext(ctx, updQ, title, searchTitle, b.Format, b.Size,
			b.Annotation, b.DocDate, b.Lang, langCodeOf(searchTitle), existingID); err != nil {
			return 0, false, fmt.Errorf("store: update book: %w", err)
		}
		return existingID, false, nil
	case errors.Is(err, sql.ErrNoRows):
		insQ := fmt.Sprintf(
			"INSERT INTO books (catalog_id, filename, path, format, size_bytes, title, search_title, "+
				"annotation, docdate, lang, lang_code) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s) RETURNING id",
			s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9), s.ph(10), s.ph(11))
		var id int64
		if err := tx.QueryRowContext(ctx, insQ, catalogID, b.Filename, path, b.Format, b.Size,
			title, searchTitle, b.Annotation, b.DocDate, b.Lang, langCodeOf(searchTitle)).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("store: insert book: %w", err)
		}
		return id, true, nil
	default:
		return 0, false, fmt.Errorf("store: lookup book: %w", err)
	}
}

// selectOrInsert ищет запись по ключу (selQ с ключом keyArg); если нет —
// вставляет через insQ (insArgs) и возвращает id. Порядок «сначала SELECT»
// безопасен для PG (в отличие от INSERT→error→SELECT в aborted-транзакции).
func (s *DBStore) selectOrInsert(ctx context.Context, tx *sql.Tx, insQ, selQ string, keyArg any, insArgs ...any) (int64, error) {
	// Подставляем %s -> актуальные плейсхолдеры ($1.. для PG, ? для SQLite).
	insQ = s.bind(insQ, len(insArgs))
	selQ = s.bind(selQ, 1)
	var id int64
	err := tx.QueryRowContext(ctx, selQ, keyArg).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("store: select: %w", err)
	}
	if err := tx.QueryRowContext(ctx, insQ, insArgs...).Scan(&id); err != nil {
		// Гонка: кто-то вставил между SELECT и INSERT — перечитать.
		if isUniqueViolation(err) {
			if err2 := tx.QueryRowContext(ctx, selQ, keyArg).Scan(&id); err2 != nil {
				return 0, fmt.Errorf("store: select after conflict: %w", err2)
			}
			return id, nil
		}
		return 0, err
	}
	return id, nil
}

// link выполняет вставку связи, игнорируя дубликаты (PK).
// Использует ON CONFLICT DO NOTHING: в PostgreSQL нельзя «проглотить» ошибку
// внутри транзакции (она станет aborted), поэтому конфликт разрешает сам SQL.
func (s *DBStore) link(ctx context.Context, tx *sql.Tx, q string, args ...any) error {
	q = s.bind(q, len(args))
	q += " ON CONFLICT DO NOTHING"
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: link: %w", err)
	}
	return nil
}

// bind заменяет %s на плейсхолдеры по порядку (PG: $1.., SQLite: ?).
// n — число аргументов в запросе.
func (s *DBStore) bind(query string, n int) string {
	parts := strings.Split(query, "%s")
	if len(parts)-1 != n {
		// Число %s не совпало с аргументами — оставить как есть (ошибка проявится).
		return query
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(parts[i])
		b.WriteString(s.ph(i + 1))
	}
	b.WriteString(parts[n])
	return b.String()
}

// langCodeOf вычисляет код набора первого символа заголовка:
// 1 — кириллица, 2 — латиница, 3 — цифра, 9 — прочее (как в старом web-интерфейсе).
func langCodeOf(title string) int {
	for _, r := range title {
		switch {
		case r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '«' || r == '“':
			continue // пропускаем обёртки/пробелы
		case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я', r == 'ё', r == 'Ё',
			r >= '\u0400' && r <= '\u04FF':
			return 1
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			return 2
		case r >= '0' && r <= '9':
			return 3
		default:
			return 9
		}
	}
	return 9
}
