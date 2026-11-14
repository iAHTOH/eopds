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
