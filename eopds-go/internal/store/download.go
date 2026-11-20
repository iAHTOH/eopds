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
