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

// ImportBook вставляет/обновляет одну книгу со связями (idempotent).
// Тонкая обёртка над ImportBooks: одна книга = пачка из одного элемента.
// Уникальность книги — по (path, filename). Авторы — по full_name, серии —
// по title, жанры — по code. Повторный импорт обновляет запись (idempotent).
func (s *DBStore) ImportBook(ctx context.Context, b IngestBook) (IngestResult, error) {
	// Для одиночной книги вам нужен её BookID — поэтому выполняем в своей
	// транзакции и достаём id. ImportBooks возвращает только агрегаты.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestResult{}, fmt.Errorf("store: import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cache := newIngestCache()
	created, err := s.importBookCore(ctx, tx, cache, b)
	if err != nil {
		return IngestResult{}, err
	}
	path := b.BookPath
	if path == "" {
		path = b.RelPath
	}
	var bookID int64
	selQ := fmt.Sprintf("SELECT id FROM books WHERE path = %s AND filename = %s", s.ph(1), s.ph(2))
	if err := tx.QueryRowContext(ctx, selQ, path, b.Filename).Scan(&bookID); err != nil {
		return IngestResult{}, fmt.Errorf("store: lookup imported book: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return IngestResult{}, fmt.Errorf("store: commit book: %w", err)
	}
	return IngestResult{BookID: bookID, Created: created}, nil
}

// ensureCatalogTree строит иерархию каталогов для пути контейнера книги и
// возвращает id непосредственного контейнера.
//
// Путь нормализуется (слэши, без "./"/".."). Каждый компонент пути — узел
// catalogs с parent_id на предыдущий уровень, path = накопленный путь,
// name = компонент. Тип (kind): leafKind применяется к ПОСЛЕДНЕМУ узлу
// (1 = zip), остальные — 0 (папки). Пустой путь/"./" → корневой каталог.
func (s *DBStore) ensureCatalogTree(ctx context.Context, tx *sql.Tx, path string, leafKind int) (int64, error) {
	path = normalizeRelPath(path)
	if path == "" || path == "." {
		return s.selectOrInsert(ctx, tx,
			"INSERT INTO catalogs (parent_id, name, path, kind) VALUES (NULL, %s, %s, 0) RETURNING id",
			"SELECT id FROM catalogs WHERE path = %s",
			".", ".", ".")
	}

	parts := strings.Split(path, "/")
	var parentID any // nil для корня
	acc := ""
	for i, part := range parts {
		if acc == "" {
			acc = part
		} else {
			acc = acc + "/" + part
		}
		kind := 0
		if i == len(parts)-1 {
			kind = leafKind
		}
		id, err := s.selectOrInsert(ctx, tx,
			"INSERT INTO catalogs (parent_id, name, path, kind) VALUES (%s, %s, %s, %s) RETURNING id",
			"SELECT id FROM catalogs WHERE path = %s",
			acc, parentID, part, acc, int64(kind))
		if err != nil {
			return 0, fmt.Errorf("store: ensure catalog %q: %w", acc, err)
		}
		parentID = id
	}
	return parentID.(int64), nil
}

// normalizeRelPath приводит путь к виду "a/b/c": прямые слэши, без ведущих/хвостовых
// слэшей, без ".", без "..". Backslash трактуется как разделитель.
func normalizeRelPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	var out []string
	for _, seg := range strings.Split(p, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, seg)
	}
	return strings.Join(out, "/")
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

// PruneCatalogs удаляет каталоги, у которых нет книг и нет дочерних каталогов
// (папки, пропавшие с диска). Повторяется до стабилизации: удаление листьев
// может опустошить и их родителей. Возвращает число удалённых узлов.
func (s *DBStore) PruneCatalogs(ctx context.Context) (int64, error) {
	var total int64
	for {
		// Удаляем листья: без книг и без детей.
		q := s.bind("DELETE FROM catalogs WHERE id NOT IN (SELECT DISTINCT catalog_id FROM books) "+
			"AND id NOT IN (SELECT DISTINCT parent_id FROM catalogs WHERE parent_id IS NOT NULL)", 0)
		res, err := s.db.ExecContext(ctx, q)
		if err != nil {
			return total, fmt.Errorf("store: prune catalogs: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
		if n == 0 {
			break
		}
	}
	return total, nil
}
