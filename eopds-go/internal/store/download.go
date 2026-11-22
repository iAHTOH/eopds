package store

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BookSource — поток с содержимым файла книги для отдачи клиенту.
// Reader нужно закрыть после использования (закрывает и ZIP-архив).
type BookSource struct {
	Reader      io.ReadCloser
	Filename    string
	ContentType string
	Size        int64 // -1, если неизвестно (например, элемент ZIP)
}

// BookFile открывает файл книги по id для скачивания: обычный файл на диске
// или элемент внутри ZIP-архива (контейнер определяется по пути).
func (s *DBStore) BookFile(ctx context.Context, id int64) (BookSource, error) {
	var path, filename, format string
	err := s.db.QueryRowContext(ctx,
		"SELECT path, filename, format FROM books WHERE id="+s.ph(1), id).
		Scan(&path, &filename, &format)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BookSource{}, ErrNotFound
		}
		return BookSource{}, err
	}
	ctype := contentTypeByFormat(format)
	// Контейнер: для ZIP — путь к самому архиву; иначе — путь к каталогу,
	// где лежит файл (path) + filename.
	zipPath := filepath.Join(s.libRoot, filepath.FromSlash(path))

	// Книга внутри ZIP: path оканчивается на .zip.
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		zr, err := zip.OpenReader(zipPath)
		if err != nil {
			return BookSource{}, fmt.Errorf("store: book file: open zip: %w", err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || !strings.EqualFold(filepath.Base(f.Name), filename) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				_ = zr.Close()
				return BookSource{}, fmt.Errorf("store: book file: zip entry: %w", err)
			}
			return BookSource{
				Reader:      &zipEntryReader{rc: rc, zr: zr},
				Filename:    filename,
				ContentType: ctype,
				Size:        int64(f.UncompressedSize64),
			}, nil
		}
		_ = zr.Close()
		return BookSource{}, ErrNotFound
	}

	// Обычный файл: <root>/<path>/<filename>.
	full := filepath.Join(zipPath, filepath.FromSlash(filename))
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return BookSource{}, ErrNotFound
		}
		return BookSource{}, fmt.Errorf("store: book file: open: %w", err)
	}
	src := BookSource{Reader: f, Filename: filename, ContentType: ctype, Size: -1}
	if fi, err := f.Stat(); err == nil {
		src.Size = fi.Size()
	}
	return src, nil
}

// zipEntryReader закрывает и элемент, и сам архив.
type zipEntryReader struct {
	rc io.ReadCloser
	zr *zip.ReadCloser
}

func (r *zipEntryReader) Read(p []byte) (int, error) { return r.rc.Read(p) }
func (r *zipEntryReader) Close() error {
	err := r.rc.Close()
	_ = r.zr.Close()
	return err
}

// contentTypeByFormat возвращает MIME-тип по расширению/формату книги.
func contentTypeByFormat(format string) string {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "fb2":
		return "application/x-fictionbook+xml"
	case "epub":
		return "application/epub+zip"
	case "mobi":
		return "application/x-mobipocket-ebook"
	case "azw", "azw3", "azw8", "kfx":
		return "application/x-mobipocket-ebook"
	case "pdf":
		return "application/pdf"
	case "txt":
		return "text/plain; charset=utf-8"
	case "md":
		return "text/markdown; charset=utf-8"
	case "zip":
		return "application/zip"
	case "djvu":
		return "image/vnd.djvu"
	default:
		return "application/octet-stream"
	}
}

// AddToShelf добавляет книгу на книжную полку пользователя (upsert) и обновляет
// время последнего обращения. Используется при скачивании книги.
func (s *DBStore) AddToShelf(ctx context.Context, userID, bookID int64) error {
	q := s.bind("INSERT INTO bookshelf (user_id, book_id, added_at, last_read_at) "+
		"VALUES (%s, %s, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) "+
		"ON CONFLICT (user_id, book_id) DO UPDATE SET last_read_at = CURRENT_TIMESTAMP", 2)
	if _, err := s.db.ExecContext(ctx, q, userID, bookID); err != nil {
		return fmt.Errorf("store: add to shelf: %w", err)
	}
	return nil
}

// SaveReadingProgress сохраняет позицию чтения книги в онлайн-читалке
// (в bookmarks.position) и обновляет время обращения на полке.
// Позиция — произвольная строка, для читалки используется "percent:12.5".
func (s *DBStore) SaveReadingProgress(ctx context.Context, userID, bookID int64, position string) error {
	// На каждую книгу держим одну «якорную» закладку прогресса.
	var bmID int64
	err := s.db.QueryRowContext(ctx,
		s.bind("SELECT id FROM bookmarks WHERE user_id=%s AND book_id=%s ORDER BY id LIMIT 1", 2),
		userID, bookID).Scan(&bmID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: reading progress lookup: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		q := s.bind("INSERT INTO bookmarks (user_id, book_id, position) VALUES (%s, %s, %s)", 3)
		if _, err := s.db.ExecContext(ctx, q, userID, bookID, position); err != nil {
			return fmt.Errorf("store: reading progress insert: %w", err)
		}
	} else {
		q := s.bind("UPDATE bookmarks SET position=%s WHERE id=%s", 2)
		if _, err := s.db.ExecContext(ctx, q, position, bmID); err != nil {
			return fmt.Errorf("store: reading progress update: %w", err)
		}
	}
	return s.AddToShelf(ctx, userID, bookID)
}

// ReadingProgress возвращает сохранённую позицию чтения (bookmarks.position).
// Если прогресса нет — пустая строка и nil.
func (s *DBStore) ReadingProgress(ctx context.Context, userID, bookID int64) (string, error) {
	var pos string
	err := s.db.QueryRowContext(ctx,
		s.bind("SELECT position FROM bookmarks WHERE user_id=%s AND book_id=%s ORDER BY id LIMIT 1", 2),
		userID, bookID).Scan(&pos)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: reading progress: %w", err)
	}
	return pos, nil
}

// ShelfBooks возвращает книги на полке пользователя, последние добавленные
// сверху (по added_at DESC, при равенстве — по book_id DESC), постранично.
func (s *DBStore) ShelfBooks(ctx context.Context, userID int64, page, size int) (LibraryPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	var total int64
	if err := s.db.QueryRowContext(ctx,
		s.bind("SELECT COUNT(*) FROM bookshelf WHERE user_id=%s", 1), userID).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("store: shelf count: %w", err)
	}

	offset := (page - 1) * size
	q := "SELECT b.id, b.title, b.format, b.size_bytes, b.lang, b.docdate, b.annotation, b.path, b.filename " +
		"FROM bookshelf s JOIN books b ON b.id = s.book_id WHERE s.user_id=" + s.ph(1) +
		" ORDER BY s.added_at DESC, b.id DESC" +
		" LIMIT " + s.ph(2) + " OFFSET " + s.ph(3)
	rows, err := s.db.QueryContext(ctx, q, userID, size, offset)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("store: shelf books: %w", err)
	}
	defer rows.Close()

	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
			&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
			return LibraryPage{}, fmt.Errorf("store: shelf scan: %w", err)
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

// RemoveFromShelf убирает книгу с полки пользователя (идемпотентно).
func (s *DBStore) RemoveFromShelf(ctx context.Context, userID, bookID int64) error {
	q := s.bind("DELETE FROM bookshelf WHERE user_id=%s AND book_id=%s", 2)
	if _, err := s.db.ExecContext(ctx, q, userID, bookID); err != nil {
		return fmt.Errorf("store: remove from shelf: %w", err)
	}
	return nil
}

// CountShelf возвращает число книг на полке пользователя (для бейджей/футера).
func (s *DBStore) CountShelf(ctx context.Context, userID int64) (int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx,
		s.bind("SELECT COUNT(*) FROM bookshelf WHERE user_id=%s", 1), userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("store: count shelf: %w", err)
	}
	return total, nil
}
