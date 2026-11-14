// Package store — слой доступа к БД для Go-ядра EOPDS.
//
// Вся SQL-логика изолирована здесь; HTTP-обработчики работают через интерфейс
// Store (см. конвенции eopds-go). Поддерживаются PostgreSQL (jackc/pgx через
// database/sql) и SQLite (modernc.org/sqlite, чистый Go, CGO выключен).
//
// При открытии БД автоматически применяются встроенные миграции (internal/
// migrate) под блокировкой, безопасной для одновременного запуска процессов.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver "pgx"
	_ "modernc.org/sqlite"             // sqlite driver "sqlite"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/migrate"
)

// Ошибки уровня store.
var (
	// ErrNotFound — запись не найдена.
	ErrNotFound = errors.New("store: not found")
	// ErrRootExists — root-админ уже создан (уникальность is_root).
	ErrRootExists = errors.New("store: root admin already exists")
)

// Store — абстракция хранилища.
type Store interface {
	// SchemaVersion возвращает текущую версию схемы.
	SchemaVersion(ctx context.Context) (int, error)

	// IsInstalled сообщает, выполнен ли мастер установки (есть root-админ).
	IsInstalled(ctx context.Context) (bool, error)

	// CreateRootAdmin создаёт единственного root-администратора.
	CreateRootAdmin(ctx context.Context, username, passwordHash, email string) (int64, error)

	// GetUserByUsername ищет пользователя по логину.
	GetUserByUsername(ctx context.Context, username string) (*User, error)

	// SetSetting сохраняет системную настройку (key/value).
	SetSetting(ctx context.Context, key, value string, updatedBy int64) error
	// GetSetting читает настройку; ErrNotFound, если нет.
	GetSetting(ctx context.Context, key string) (string, error)

	// Stats возвращает сводную статистику для дашборда.
	Stats(ctx context.Context) (Stats, error)

	// CreateSession создаёт сессию пользователя.
	CreateSession(ctx context.Context, id string, userID int64, expiresAt time.Time) error
	// GetSessionUser возвращает пользователя активной сессии (ErrNotFound).
	GetSessionUser(ctx context.Context, id string) (*User, error)
	// DeleteSession удаляет сессию (logout).
	DeleteSession(ctx context.Context, id string) error
	// PurgeExpiredSessions удаляет истёкшие сессии.
	PurgeExpiredSessions(ctx context.Context) error

	// Ping проверяет соединение с БД.
	Ping(ctx context.Context) error

	// Close закрывает БД.
	Close() error
}

// Stats — счётчики для дашборда админки.
type Stats struct {
	Books   int64
	Authors int64
	Series  int64
	Users   int64
}

// User — пользователь системы.
type User struct {
	ID           int64
	Username     string
	Email        *string
	PasswordHash string
	Role         string
	IsRoot       bool
	IsActive     bool
	// CreatedAt — строка в формате RFC3339 (одинаково для PG/SQLite).
	CreatedAt string
}

// DBStore — реализация Store поверх database/sql.
type DBStore struct {
	db   *sql.DB
	kind config.DBType
}

// openDB открывает database/sql для указанной конфигурации.
func openDB(cfg config.Config) (*sql.DB, error) {
	switch cfg.DBType {
	case config.DBPostgres:
		db, err := sql.Open("pgx", cfg.PostgresDSN())
		if err != nil {
			return nil, fmt.Errorf("store: open postgres: %w", err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(time.Hour)
		return db, nil
	case config.DBSQLite:
		db, err := sql.Open("sqlite", cfg.SQLiteDSN())
		if err != nil {
			return nil, fmt.Errorf("store: open sqlite: %w", err)
		}
		// SQLite: один писатель; ограничим пул для предсказуемости.
		db.SetMaxOpenConns(1)
		return db, nil
	default:
		return nil, fmt.Errorf("store: unsupported DB type %q", cfg.DBType)
	}
}

// migrateDialect сопоставляет тип БД диалекту миграций.
func migrateDialect(kind config.DBType) migrate.Dialect {
	if kind == config.DBSQLite {
		return migrate.DialectSQLite
	}
	return migrate.DialectPostgres
}

// Open открывает БД, применяет миграции и возвращает Store.
// Если БД недоступна — возвращает ошибку (вызывающая сторона решает, что делать).
func Open(ctx context.Context, cfg config.Config, log migrate.Logger) (Store, error) {
	db, err := openDB(cfg)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	if err := applyMigrations(ctx, db, cfg.DBType, log); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &DBStore{db: db, kind: cfg.DBType}, nil
}

// applyMigrations применяет миграции под блокировкой.
func applyMigrations(ctx context.Context, db *sql.DB, kind config.DBType, log migrate.Logger) error {
	unlock, err := lockMigrations(ctx, db, kind)
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := migrate.Apply(ctx, db, migrateDialect(kind), log); err != nil {
		return err
	}
	return nil
}

// lockMigrations захватывает блокировку миграций.
// PostgreSQL — advisory lock; SQLite — сериализация через один коннект (MaxOpenConns=1).
func lockMigrations(ctx context.Context, db *sql.DB, kind config.DBType) (func(), error) {
	if kind != config.DBPostgres {
		// SQLite с MaxOpenConns(1) выполняет операции последовательно.
		return func() {}, nil
	}
	const lockKey = 0x656f7064 // "eopd"
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: migration lock conn: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("store: pg_advisory_lock: %w", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
		_ = conn.Close()
	}, nil
}

// SchemaVersion возвращает версию схемы.
func (s *DBStore) SchemaVersion(ctx context.Context) (int, error) {
	return migrate.CurrentVersion(ctx, s.db, migrateDialect(s.kind))
}

// Ping проверяет соединение.
func (s *DBStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Stats возвращает количество книг, авторов, серий и пользователей.
// Пустая/незаполненная БД даёт нули; ошибка только при сбое запроса.
func (s *DBStore) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	queries := []struct {
		q   string
		dst *int64
	}{
		{"SELECT COUNT(*) FROM books", &st.Books},
		{"SELECT COUNT(*) FROM authors", &st.Authors},
		{"SELECT COUNT(*) FROM series", &st.Series},
		{"SELECT COUNT(*) FROM users", &st.Users},
	}
	for _, item := range queries {
		if err := s.db.QueryRowContext(ctx, item.q).Scan(item.dst); err != nil {
			return Stats{}, fmt.Errorf("store: stats: %w", err)
		}
	}
	return st, nil
}

// Close закрывает БД.
func (s *DBStore) Close() error {
	return s.db.Close()
}

// ph возвращает placeholder для N-го аргумента ($N для PG, ? для SQLite).
func (s *DBStore) ph(n int) string {
	if s.kind == config.DBSQLite {
		return "?"
	}
	return fmt.Sprintf("$%d", n)
}
