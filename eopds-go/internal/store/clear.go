package store

import (
	"context"
	"fmt"
)

// ClearResult — счётчики удалённых записей после очистки библиотеки.
type ClearResult struct {
	Books    int64
	Authors  int64
	Series   int64
	Genres   int64
	Catalogs int64
}

// ClearLibrary полностью очищает каталог книг: книги и связанные сущности
// (авторы, серии, жанры, каталоги) вместе со связями и производными данными
// (обложки, закладки, кэш аннотаций, скачивания, полка).
//
// НЕ трогает: пользователей, настройки, сессии, счётчики, журнал аудита.
// Всё выполняется в одной транзакции — либо целиком, либо никак.
func (s *DBStore) ClearLibrary(ctx context.Context) (ClearResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClearResult{}, fmt.Errorf("store: clear: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Порядок удаления — от зависимых к базовым (FK ON DELETE не везде CASCADE).
	tables := []string{
		"book_covers",
		"bookmarks",
		"annotations_cache",
		"downloads",
		"bookshelf",
		"book_authors",
		"book_genres",
		"book_series",
	}
	for _, t := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return ClearResult{}, fmt.Errorf("store: clear %s: %w", t, err)
		}
	}

	var res ClearResult
	for _, item := range []struct {
		table string
		dst   *int64
	}{
		{"books", &res.Books},
		{"authors", &res.Authors},
		{"series", &res.Series},
		{"genres", &res.Genres},
		{"catalogs", &res.Catalogs},
	} {
		r, err := tx.ExecContext(ctx, "DELETE FROM "+item.table)
		if err != nil {
			return ClearResult{}, fmt.Errorf("store: clear %s: %w", item.table, err)
		}
		n, _ := r.RowsAffected()
		*item.dst = n
	}

	if err := tx.Commit(); err != nil {
		return ClearResult{}, fmt.Errorf("store: clear: commit: %w", err)
	}
	return res, nil
}
