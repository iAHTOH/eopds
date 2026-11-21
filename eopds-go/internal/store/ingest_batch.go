package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// BatchResult — агрегированный итог пакетного импорта.
type BatchResult struct {
	Created int64
	Updated int64
	Errors  int64
}

// ingestCache — кэши справочников в пределах одной транзакции-пачки.
// Позволяет не делать повторные SELECT для уже виденных авторов/серий/
// жанров/каталогов (в коллекции тысячи повторов).
type ingestCache struct {
	authors  map[string]int64 // full_name -> id
	series   map[string]int64 // title -> id
	genres   map[string]int64 // code -> id
	catalogs map[string]int64 // path -> id
}

func newIngestCache() *ingestCache {
	return &ingestCache{
		authors:  make(map[string]int64),
		series:   make(map[string]int64),
		genres:   make(map[string]int64),
		catalogs: make(map[string]int64),
	}
}

// ImportBooks импортирует пачку книг в ОДНОЙ транзакции.
//
// Зачем: при первичном сканировании больших коллекций (тысячи книг) одна
// транзакция на книгу даёт лишний round-trip на BEGIN/COMMIT. Здесь все книги
// пачки пишутся одной транзакцией, а каждая книга обёрнута в SAVEPOINT:
// ошибка одной книги откатывает только её, остальные сохраняются.
//
// Возвращает число созданных/обновлённых книг и книг с ошибками. Ошибка —
// только при сбое самой транзакции (BEGIN/COMMIT) или отмене ctx; ошибки
// отдельных книг учитываются в Errors и не прерывают пачку.
func (s *DBStore) ImportBooks(ctx context.Context, books []IngestBook) (BatchResult, error) {
	var res BatchResult
	if len(books) == 0 {
		return res, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: import batch tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cache := newIngestCache()
	spN := 0
	nextSavepoint := func() string {
		spN++
		return fmt.Sprintf("bk_%d", spN)
	}

	for _, b := range books {
		if err := ctx.Err(); err != nil {
			return res, err // прервано — транзакция откатится целиком
		}
		created, err := s.importOne(ctx, tx, cache, b, nextSavepoint)
		if err != nil {
			res.Errors++
			continue
		}
		if created {
			res.Created++
		} else {
			res.Updated++
		}
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("store: commit batch: %w", err)
	}
	return res, nil
}

// importOne импортирует одну книгу внутри переданной транзакции,
// оборачивая её в SAVEPOINT для изоляции ошибок.
func (s *DBStore) importOne(ctx context.Context, tx *sql.Tx, cache *ingestCache, b IngestBook, nextSavepoint func() string) (bool, error) {
	sp := nextSavepoint()
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+sp); err != nil {
		return false, fmt.Errorf("store: savepoint: %w", err)
	}
	created, err := s.importBookCore(ctx, tx, cache, b)
	if err != nil {
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
			return false, fmt.Errorf("store: rollback savepoint: %w (orig: %v)", rbErr, err)
		}
		_, _ = tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp)
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
		return false, fmt.Errorf("store: release savepoint: %w", err)
	}
	return created, nil
}

// importBookCore — тело вставки одной книги в уже открытой транзакции.
// Возвращает created=true, если книга добавлена (а не обновлена).
func (s *DBStore) importBookCore(ctx context.Context, tx *sql.Tx, cache *ingestCache, b IngestBook) (bool, error) {
	path := b.BookPath
	if path == "" {
		path = b.RelPath
	}
	title := strings.TrimSpace(b.Title)
	if title == "" {
		title = b.Filename
	}
	searchTitle := strings.ToLower(title)

	// Каталог книги (с кэшем путей в пределах пачки).
	catalogID, err := s.ensureCatalogTreeCached(ctx, tx, cache, path, b.CatalogType)
	if err != nil {
		return false, err
	}

	// UPSERT книги по (path, filename).
	bookID, created, err := s.upsertBook(ctx, tx, catalogID, b, path, title, searchTitle)
	if err != nil {
		return false, err
	}

	// Авторы (кэш full_name -> id).
	for _, name := range b.Authors {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		authorID, err := s.selectOrInsertCached(ctx, tx, cache.authors, name,
			"INSERT INTO authors (full_name, search_full_name) VALUES (%s, %s) RETURNING id",
			"SELECT id FROM authors WHERE full_name = %s",
			name, name, strings.ToLower(name))
		if err != nil {
			return false, fmt.Errorf("store: upsert author: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_authors (book_id, author_id) VALUES (%s, %s)",
			bookID, authorID); err != nil {
			return false, err
		}
	}

	// Серия (кэш title -> id).
	if st := strings.TrimSpace(b.SeriesTitle); st != "" {
		seriesID, err := s.selectOrInsertCached(ctx, tx, cache.series, st,
			"INSERT INTO series (title, search_title) VALUES (%s, %s) RETURNING id",
			"SELECT id FROM series WHERE title = %s",
			st, st, strings.ToLower(st))
		if err != nil {
			return false, fmt.Errorf("store: upsert series: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_series (book_id, series_id, seq_no) VALUES (%s, %s, %s)",
			bookID, seriesID, int64(b.SeriesIndex)); err != nil {
			return false, err
		}
	}

	// Жанры (кэш code -> id).
	for _, code := range b.Genres {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		genreID, err := s.selectOrInsertCached(ctx, tx, cache.genres, code,
			"INSERT INTO genres (code) VALUES (%s) RETURNING id",
			"SELECT id FROM genres WHERE code = %s",
			code, code)
		if err != nil {
			return false, fmt.Errorf("store: upsert genre: %w", err)
		}
		if err := s.link(ctx, tx,
			"INSERT INTO book_genres (book_id, genre_id) VALUES (%s, %s)",
			bookID, genreID); err != nil {
			return false, err
		}
	}

	return created, nil
}

// selectOrInsertCached — как selectOrInsert, но с кэшем id по ключу в пачке.
func (s *DBStore) selectOrInsertCached(ctx context.Context, tx *sql.Tx, cache map[string]int64, key, insQ, selQ string, keyArg any, insArgs ...any) (int64, error) {
	if id, ok := cache[key]; ok {
		return id, nil
	}
	id, err := s.selectOrInsert(ctx, tx, insQ, selQ, keyArg, insArgs...)
	if err != nil {
		return 0, err
	}
	cache[key] = id
	return id, nil
}

// ensureCatalogTreeCached — как ensureCatalogTree, но с кэшем path -> id в пачке.
// Промежуточные узлы кэшируются (их kind=0 уже фиксирован); листовой узел
// кэшируется по полному нормализованному пути.
func (s *DBStore) ensureCatalogTreeCached(ctx context.Context, tx *sql.Tx, cache *ingestCache, path string, leafKind int) (int64, error) {
	norm := normalizeRelPath(path)
	if norm == "" || norm == "." {
		return s.ensureCatalogTree(ctx, tx, path, leafKind)
	}
	if id, ok := cache.catalogs[norm]; ok {
		return id, nil
	}

	parts := strings.Split(norm, "/")
	var parentID any // nil для корня
	acc := ""
	var lastID int64
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
		// Промежуточный узел уже создан ранее — берём id из кэша.
		if i < len(parts)-1 {
			if id, ok := cache.catalogs[acc]; ok {
				parentID = id
				lastID = id
				continue
			}
		}
		id, err := s.selectOrInsert(ctx, tx,
			"INSERT INTO catalogs (parent_id, name, path, kind) VALUES (%s, %s, %s, %s) RETURNING id",
			"SELECT id FROM catalogs WHERE path = %s",
			acc, parentID, part, acc, int64(kind))
		if err != nil {
			return 0, fmt.Errorf("store: ensure catalog %q: %w", acc, err)
		}
		if i < len(parts)-1 {
			cache.catalogs[acc] = id
		}
		parentID = id
		lastID = id
	}
	cache.catalogs[norm] = lastID
	return lastID, nil
}
