package migrate

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

type nopLogger struct{}

func (nopLogger) Infof(string, string, ...any)  {}
func (nopLogger) Debugf(string, string, ...any) {}

// TestApplySQLite проверяет, что baseline-миграции создают схему на SQLite,
// повторный Apply идемпотентен, а ключевые таблицы существуют.
func TestApplySQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	// Один коннект, иначе in-memory БД «теряется» между соединениями.
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	n, err := Apply(ctx, db, DialectSQLite, nopLogger{})
	if err != nil {
		t.Fatalf("apply #1: %v", err)
	}
	if n == 0 {
		t.Fatalf("apply #1: expected migrations to be applied, got 0")
	}

	// Повторный запуск — без изменений.
	n2, err := Apply(ctx, db, DialectSQLite, nopLogger{})
	if err != nil {
		t.Fatalf("apply #2: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("apply #2: expected 0 applied, got %d", n2)
	}

	// Проверяем наличие таблиц.
	for _, tbl := range []string{
		"app_meta", "schema_migrations", "counters", "scan_runs",
		"catalogs", "books", "authors", "book_authors", "genres",
		"book_genres", "series", "book_series", "users", "invites",
		"sessions", "bookshelf", "bookmarks", "annotations_cache",
		"book_covers", "downloads", "registration_requests", "audit_log", "settings",
		"scan_files", "system_events", "book_ratings", "book_comments",
	} {
		ok, err := tableExists(ctx, db, DialectSQLite, tbl)
		if err != nil {
			t.Fatalf("tableExists %s: %v", tbl, err)
		}
		if !ok {
			t.Errorf("table %q not created", tbl)
		}
	}

	// Версия схемы.
	v, err := CurrentVersion(ctx, db, DialectSQLite)
	if err != nil {
		t.Fatalf("current version: %v", err)
	}
	if v != 7 {
		t.Errorf("schema_version = %d, want 7", v)
	}

	// Ограничение «ровно один root».
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, role, is_root) VALUES ('root1', 'admin', 1)`); err != nil {
		t.Fatalf("insert root1: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, role, is_root) VALUES ('root2', 'admin', 1)`); err == nil {
		t.Errorf("expected second root to violate unique index")
	}
}
