package store

import (
	"context"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
)

// nopLogger — пустой логгер для миграций в тестах.
type nopLogger struct{}

func (nopLogger) Infof(module, format string, args ...any)  {}
func (nopLogger) Debugf(module, format string, args ...any) {}

// newTestStore открывает SQLite in-memory с применёнными миграциями.
func newTestStore(t *testing.T) Store {
	t.Helper()
	cfg := config.Config{DBType: config.DBSQLite, DBPath: "file::memory:?cache=shared"}
	st, err := Open(context.Background(), cfg, nopLogger{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestImportBookIdempotent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	b := IngestBook{
		RelPath:     "/lib/a",
		BookPath:    "/lib/a",
		Filename:    "one.fb2",
		Format:      "fb2",
		Size:        123,
		Title:       "Книга",
		Lang:        "ru",
		Authors:     []string{"Иван Петров"},
		Genres:      []string{"sf"},
		SeriesTitle: "Серия",
		SeriesIndex: 2,
	}

	r1, err := st.ImportBook(ctx, b)
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	if !r1.Created {
		t.Fatalf("первый импорт должен создать книгу")
	}

	// Повторный импорт той же книги — обновление, без дублей.
	r2, err := st.ImportBook(ctx, b)
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if r2.Created {
		t.Fatalf("повторный импорт не должен создавать новую книгу")
	}
	if r2.BookID != r1.BookID {
		t.Fatalf("book id изменился: %d -> %d", r1.BookID, r2.BookID)
	}

	s, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.Books != 1 || s.Authors != 1 || s.Series != 1 {
		t.Fatalf("ожидалось 1/1/1, получено books=%d authors=%d series=%d", s.Books, s.Authors, s.Series)
	}
}

func TestBind(t *testing.T) {
	pg := &DBStore{kind: config.DBPostgres}
	if got := pg.bind("a = %s AND b = %s", 2); got != "a = $1 AND b = $2" {
		t.Errorf("pg bind = %q", got)
	}
	sq := &DBStore{kind: config.DBSQLite}
	if got := sq.bind("a = %s", 1); got != "a = ?" {
		t.Errorf("sqlite bind = %q", got)
	}
	// несовпадение числа плейсхолдеров — возврат как есть
	if got := pg.bind("x = %s", 2); got != "x = %s" {
		t.Errorf("mismatch bind = %q", got)
	}
}

func TestImportBooksBatch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	books := []IngestBook{
		{RelPath: "/lib/a", BookPath: "/lib/a", Filename: "1.fb2", Format: "fb2", Size: 1, Title: "Книга 1", Authors: []string{"Иван Петров"}, Genres: []string{"sf"}, SeriesTitle: "Серия", SeriesIndex: 1},
		{RelPath: "/lib/a", BookPath: "/lib/a", Filename: "2.fb2", Format: "fb2", Size: 2, Title: "Книга 2", Authors: []string{"Иван Петров", "Пётр Сидоров"}, Genres: []string{"sf", "det"}, SeriesTitle: "Серия", SeriesIndex: 2},
		{RelPath: "/lib/b", BookPath: "/lib/b", Filename: "3.fb2", Format: "fb2", Size: 3, Title: "", Authors: nil, Genres: nil},
	}

	res, err := st.ImportBooks(ctx, books)
	if err != nil {
		t.Fatalf("import batch: %v", err)
	}
	if res.Created != 3 || res.Updated != 0 || res.Errors != 0 {
		t.Fatalf("ожидалось created=3 updated=0 errors=0, получено %+v", res)
	}

	// Повторный батч — всё обновление, без дублей.
	res2, err := st.ImportBooks(ctx, books)
	if err != nil {
		t.Fatalf("import batch 2: %v", err)
	}
	if res2.Created != 0 || res2.Updated != 3 || res2.Errors != 0 {
		t.Fatalf("повтор: ожидалось created=0 updated=3 errors=0, получено %+v", res2)
	}

	s, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 3 книги, 2 автора, 1 серия, 2 жанра (жанры не входят в Stats).
	if s.Books != 3 || s.Authors != 2 || s.Series != 1 {
		t.Fatalf("ожидалось books=3 authors=2 series=1, получено %+v", s)
	}
}

func TestImportBooksErrorIsolation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Вторая книга «битая»: пустой filename → уникальный индекс допускает,
	// но NOT NULL/логика — проверим изоляцию, сделав filename пустым у одной
	// и валидным у соседей; пустой filename не уникален по индексу, но
	// импорт не должен валить остальные книги пачки.
	books := []IngestBook{
		{RelPath: "/x", BookPath: "/x", Filename: "ok1.fb2", Title: "OK 1"},
		{RelPath: "/x", BookPath: "/x", Filename: "", Title: "Пустое имя"},
		{RelPath: "/x", BookPath: "/x", Filename: "ok2.fb2", Title: "OK 2"},
	}
	res, err := st.ImportBooks(ctx, books)
	if err != nil {
		t.Fatalf("batch не должен падать целиком: %v", err)
	}
	// Валидные книги должны сохраниться даже если одна «плохая».
	s, _ := st.Stats(ctx)
	if s.Books < 2 {
		t.Fatalf("валидные книги потерялись: books=%d (res=%+v)", s.Books, res)
	}
}
