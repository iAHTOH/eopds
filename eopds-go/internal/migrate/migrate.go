// Package migrate — встроенные миграции схемы БД для Go-ядра EOPDS.
//
// Миграции — упорядоченные SQL-файлы, встроенные в бинарник через go:embed.
// Отдельные наборы для PostgreSQL (migrations/pg) и SQLite (migrations/sqlite).
//
// Применение (см. Apply) идемпотентно:
//   - пустая БД    → создаётся вся схема (baseline + последующие миграции);
//   - есть миграции, которых нет в БД → докатываются по одной, каждая в своей
//     транзакции;
//   - повторный запуск ничего не делает.
//
// Вызывающая сторона (пакет store) обеспечивает блокировку от одновременного
// применения миграций несколькими процессами.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/pg/*.sql migrations/sqlite/*.sql
var migrationsFS embed.FS

// Dialect — СУБД, для которой применяются миграции.
type Dialect string

const (
	DialectPostgres Dialect = "pg"
	DialectSQLite   Dialect = "sqlite"
)

// Logger — минимальный интерфейс логирования (совместим с logx.Logger).
type Logger interface {
	Infof(module, format string, args ...any)
	Debugf(module, format string, args ...any)
}

// migration — одна миграция (файл).
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations читает встроенные миграции для указанного диалекта.
func loadMigrations(d Dialect) ([]migration, error) {
	if d != DialectPostgres && d != DialectSQLite {
		return nil, fmt.Errorf("migrate: unsupported dialect %q", d)
	}
	dir := "migrations/" + string(d)
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", dir, err)
	}

	var out []migration
	seen := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Имя файла: NNNN_description.sql
		base := strings.TrimSuffix(e.Name(), ".sql")
		underscore := strings.IndexByte(base, '_')
		if underscore <= 0 {
			return nil, fmt.Errorf("migrate: bad migration filename %q (want NNNN_desc.sql)", e.Name())
		}
		version, err := strconv.Atoi(base[:underscore])
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrate: bad version in %q: %v", e.Name(), err)
		}
		if prev, ok := seen[version]; ok {
			return nil, fmt.Errorf("migrate: duplicate version %d in %q and %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		b, err := migrationsFS.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: e.Name(), sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// tableExists проверяет наличие таблицы (диалект-специфично).
func tableExists(ctx context.Context, db *sql.DB, d Dialect, name string) (bool, error) {
	var q string
	switch d {
	case DialectPostgres:
		q = `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`
	case DialectSQLite:
		q = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`
	default:
		return false, fmt.Errorf("migrate: unsupported dialect %q", d)
	}
	var ok bool
	if err := db.QueryRowContext(ctx, q, name).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// appliedVersions возвращает уже применённые версии.
func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// recordApplied фиксирует миграцию в schema_migrations.
func recordApplied(ctx context.Context, tx *sql.Tx, d Dialect, version int, name string) error {
	q := `INSERT INTO schema_migrations (version, description) VALUES ($1, $2)`
	if d == DialectSQLite {
		q = `INSERT INTO schema_migrations (version, description) VALUES (?, ?)`
	}
	_, err := tx.ExecContext(ctx, q, version, name)
	return err
}

// Apply применяет все недостающие миграции для диалекта.
// Возвращает число реально применённых миграций.
func Apply(ctx context.Context, db *sql.DB, d Dialect, log Logger) (int, error) {
	migs, err := loadMigrations(d)
	if err != nil {
		return 0, err
	}
	if len(migs) == 0 {
		return 0, fmt.Errorf("migrate: no migrations embedded for dialect %q", d)
	}

	has, err := tableExists(ctx, db, d, "schema_migrations")
	if err != nil {
		return 0, fmt.Errorf("migrate: check schema_migrations: %w", err)
	}

	applied := make(map[int]bool)
	if has {
		applied, err = appliedVersions(ctx, db)
		if err != nil {
			return 0, fmt.Errorf("migrate: read applied versions: %w", err)
		}
	}

	count := 0
	for _, m := range migs {
		if applied[m.version] {
			continue
		}

		// Baseline (версия 1) при пустой БД выполняется как есть: он создаёт
		// schema_migrations и сразу пишет в неё запись. Для последующих миграций
		// фиксируем в той же транзакции.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return count, fmt.Errorf("migrate: begin v%d: %w", m.version, err)
		}

		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return count, fmt.Errorf("migrate: exec v%d (%s): %w", m.version, m.name, err)
		}

		// Baseline-файл сам вставляет запись в schema_migrations и app_meta;
		// повторная запись не нужна и вызовет конфликт по PK.
		if has || m.version != 1 {
			if err := recordApplied(ctx, tx, d, m.version, m.name); err != nil {
				_ = tx.Rollback()
				return count, fmt.Errorf("migrate: record v%d: %w", m.version, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return count, fmt.Errorf("migrate: commit v%d: %w", m.version, err)
		}

		if log != nil {
			log.Infof("migrate", "applied %s", m.name)
		}
		count++
		has = true // после первой миграции schema_migrations существует
	}

	if log != nil {
		if count == 0 {
			log.Infof("migrate", "schema up to date (dialect=%s)", d)
		} else {
			log.Infof("migrate", "schema updated: %d migration(s) (dialect=%s)", count, d)
		}
	}
	return count, nil
}

// CurrentVersion возвращает текущую версию схемы из app_meta.
// Если app_meta нет — возвращает 0.
func CurrentVersion(ctx context.Context, db *sql.DB, d Dialect) (int, error) {
	has, err := tableExists(ctx, db, d, "app_meta")
	if err != nil {
		return 0, err
	}
	if !has {
		return 0, nil
	}
	var v int
	if err := db.QueryRowContext(ctx, `SELECT schema_version FROM app_meta WHERE id = 1`).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}
