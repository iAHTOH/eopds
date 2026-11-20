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

	"github.com/iAHTOH/eopds/eopds-go/internal/fb2"
)

// Cover — обложка книги для отдачи HTTP-клиенту.
type Cover struct {
	Data        []byte
	ContentType string
}

// BookCover возвращает обложку книги по id.
//
// Извлечение ленивое: сначала проверяется файловый кэш (<cacheDir>/<id>.<ext>);
// при промахе обложка извлекается из файла книги (FB2, в т.ч. внутри ZIP) и
// кладётся в кэш + регистрируется в book_covers. Для не-FB2/без обложки
// возвращается ErrNoCover.
func (s *DBStore) BookCover(ctx context.Context, id int64) (Cover, error) {
	// 1) Кэш на диске.
	if c, ok := s.readCoverCache(id); ok {
		return c, nil
	}

	// 2) Метаданные книги (путь/имя/формат).
	var path, filename, format string
	err := s.db.QueryRowContext(ctx,
		"SELECT path, filename, format FROM books WHERE id="+s.ph(1), id).
		Scan(&path, &filename, &format)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Cover{}, ErrNotFound
		}
		return Cover{}, err
	}
	if !strings.EqualFold(format, "fb2") {
		return Cover{}, fb2.ErrNoCover
	}

	data, ctype, err := s.extractCoverFromFile(path, filename)
	if err != nil {
		return Cover{}, err
	}

	c := Cover{Data: data, ContentType: ctype}
	s.writeCoverCache(id, c)    // best-effort
	s.registerCover(ctx, id, c) // best-effort
	return c, nil
}

// extractCoverFromFile открывает книгу (файл или элемент ZIP) и извлекает обложку.
func (s *DBStore) extractCoverFromFile(path, filename string) ([]byte, string, error) {
	full := filepath.Join(s.libRoot, filepath.FromSlash(path))

	// Книга внутри ZIP: path оканчивается на .zip (или filename внутри архива).
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		zr, err := zip.OpenReader(full)
		if err != nil {
			return nil, "", fmt.Errorf("store: cover: open zip: %w", err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			if !strings.EqualFold(filepath.Base(f.Name), filename) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, "", fmt.Errorf("store: cover: zip entry: %w", err)
			}
			defer rc.Close()
			return decodeCover(rc)
		}
		return nil, "", fb2.ErrNoCover
	}

	// Обычный файл: <root>/<path>/<filename>.
	f, err := os.Open(filepath.Join(full, filepath.FromSlash(filename)))
	if err != nil {
		return nil, "", fmt.Errorf("store: cover: open file: %w", err)
	}
	defer f.Close()
	return decodeCover(f)
}

// decodeCover извлекает обложку из потока FB2.
func decodeCover(r io.Reader) ([]byte, string, error) {
	c, err := fb2.ExtractCover(r)
	if err != nil {
		return nil, "", err
	}
	return c.Data, c.ContentType, nil
}

// coverExtByType возвращает расширение по Content-Type.
func coverExtByType(ct string) string {
	switch strings.ToLower(ct) {
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "jpg"
	}
}

// readCoverCache ищет обложку в файловом кэше (любое из поддерживаемых расширений).
func (s *DBStore) readCoverCache(id int64) (Cover, bool) {
	if s.cacheDir == "" {
		return Cover{}, false
	}
	for _, ext := range []string{"jpg", "png", "gif", "webp"} {
		p := filepath.Join(s.cacheDir, fmt.Sprintf("%d.%s", id, ext))
		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 {
			continue
		}
		return Cover{Data: data, ContentType: "image/" + ext}, true
	}
	return Cover{}, false
}

// writeCoverCache сохраняет обложку в файловый кэш (best-effort).
func (s *DBStore) writeCoverCache(id int64, c Cover) {
	if s.cacheDir == "" || len(c.Data) == 0 {
		return
	}
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return
	}
	p := filepath.Join(s.cacheDir, fmt.Sprintf("%d.%s", id, coverExtByType(c.ContentType)))
	_ = os.WriteFile(p, c.Data, 0o644)
}

// registerCover записывает факт наличия обложки в book_covers (best-effort).
func (s *DBStore) registerCover(ctx context.Context, id int64, c Cover) {
	q := "INSERT INTO book_covers (book_id, kind, source, format, size_bytes, file_path) " +
		"VALUES (" + s.ph(1) + ", 2, 'extracted', " + s.ph(2) + ", " + s.ph(3) + ", " + s.ph(4) + ")"
	_, _ = s.db.ExecContext(ctx, q, id, coverExtByType(c.ContentType), len(c.Data), "")
}
