package store

import (
	"context"
	"fmt"
)

// FileFingerprint — отпечаток файла для инкрементального скана.
// Size — размер в байтах, MTime — unix-время модификации в наносекундах.
type FileFingerprint struct {
	Size  int64
	MTime int64
}

// ScanFiles возвращает отпечатки всех ранее прочитанных файлов/архивов.
// Ключ — относительный путь (книга: rel_dir/filename; архив: rel_dir/name).
func (s *DBStore) ScanFiles(ctx context.Context) (map[string]FileFingerprint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, size, mtime FROM scan_files`)
	if err != nil {
		return nil, fmt.Errorf("store: scan files: %w", err)
	}
	defer rows.Close()

	out := make(map[string]FileFingerprint)
	for rows.Next() {
		var p string
		var fp FileFingerprint
		if err := rows.Scan(&p, &fp.Size, &fp.MTime); err != nil {
			return nil, fmt.Errorf("store: scan files scan: %w", err)
		}
		out[p] = fp
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan files rows: %w", err)
	}
	return out, nil
}

// SaveScanFiles сохраняет отпечатки пачки файлов (upsert по path).
// Используется после успешного импорта, чтобы следующий скан их пропустил.
func (s *DBStore) SaveScanFiles(ctx context.Context, files map[string]FileFingerprint) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: save scan files tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := s.bind(
		`INSERT INTO scan_files (path, size, mtime) VALUES (%s, %s, %s) `+
			`ON CONFLICT (path) DO UPDATE SET size = EXCLUDED.size, mtime = EXCLUDED.mtime`, 3)
	for p, fp := range files {
		if _, err := tx.ExecContext(ctx, q, p, fp.Size, fp.MTime); err != nil {
			return fmt.Errorf("store: upsert scan file %q: %w", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit scan files: %w", err)
	}
	return nil
}

// ClearScanFiles удаляет все отпечатки (например, при очистке библиотеки).
func (s *DBStore) ClearScanFiles(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scan_files`); err != nil {
		return fmt.Errorf("store: clear scan files: %w", err)
	}
	return nil
}
