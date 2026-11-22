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
	"path/filepath"
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

	// ListUsers возвращает всех пользователей.
	ListUsers(ctx context.Context) ([]User, error)

	// SetUserPasswordHash обновляет хеш пароля пользователя по id.
	SetUserPasswordHash(ctx context.Context, id int64, passwordHash string) error

	// ImportBook вставляет/обновляет книгу со связями (idempotent).
	ImportBook(ctx context.Context, b IngestBook) (IngestResult, error)

	// ImportBooks пакетно импортирует книги в одной транзакции (per-book SAVEPOINT).
	ImportBooks(ctx context.Context, books []IngestBook) (BatchResult, error)

	// ClearLibrary полностью очищает каталог книг (книги, авторы, серии,
	// жанры, каталоги + связи/производные). Пользователи и настройки — сохраняются.
	ClearLibrary(ctx context.Context) (ClearResult, error)
	// PruneCatalogs удаляет пустые каталоги (без книг и детей); возвращает число.
	PruneCatalogs(ctx context.Context) (int64, error)

	// ScanFiles возвращает отпечатки (size,mtime) прочитанных файлов/архивов.
	ScanFiles(ctx context.Context) (map[string]FileFingerprint, error)
	// SaveScanFiles сохраняет отпечатки пачки файлов (upsert по path).
	SaveScanFiles(ctx context.Context, files map[string]FileFingerprint) error
	// ClearScanFiles удаляет все отпечатки (при очистке библиотеки).
	ClearScanFiles(ctx context.Context) error
	// Каталоги.
	ListRootCatalogs(ctx context.Context) ([]Catalog, error)
	GetCatalog(ctx context.Context, id int64) (Catalog, error)
	ListChildCatalogs(ctx context.Context, parentID int64) ([]Catalog, error)
	ListBooksInCatalog(ctx context.Context, catalogID int64, page, size int) (LibraryPage, error)
	// SuggestBooks — быстрые подсказки для автопоиска (by: title|author|series).
	SuggestBooks(ctx context.Context, q, by string, limit int) ([]LibraryBook, error)
	// BookFile открывает файл книги (на диске или в ZIP) для скачивания.
	BookFile(ctx context.Context, id int64) (BookSource, error)
	// AddToShelf добавляет книгу на полку пользователя (обновляет last_read_at).
	AddToShelf(ctx context.Context, userID, bookID int64) error
	// SaveReadingProgress сохраняет позицию чтения в онлайн-читалке.
	SaveReadingProgress(ctx context.Context, userID, bookID int64, position string) error
	// ReadingProgress возвращает сохранённую позицию чтения ("" если нет).
	ReadingProgress(ctx context.Context, userID, bookID int64) (string, error)

	// SetSetting сохраняет системную настройку (key/value).
	SetSetting(ctx context.Context, key, value string, updatedBy int64) error
	// GetSetting читает настройку; ErrNotFound, если нет.
	GetSetting(ctx context.Context, key string) (string, error)

	// Stats возвращает сводную статистику для дашборда.
	Stats(ctx context.Context) (Stats, error)
	// ListBooks возвращает страницу книг по фильтру (алфавит/автор/серия/жанр).
	ListBooks(ctx context.Context, f BooksFilter, page, size int) (LibraryPage, error)
	// SearchBooks ищет книги по подстроке названия, постранично.
	SearchBooks(ctx context.Context, q string, page, size int) (LibraryPage, error)
	// GetBook возвращает книгу со связями по id.
	GetBook(ctx context.Context, id int64) (LibraryBook, error)
	// BookCover возвращает обложку книги (ленивое извлечение из файла, кэш).
	BookCover(ctx context.Context, id int64) (Cover, error)
	// BookAlphabet/AuthorAlphabet/SeriesAlphabet — алфавитные указатели.
	BookAlphabet(ctx context.Context) ([]AlphabetItem, error)
	AuthorAlphabet(ctx context.Context) ([]AlphabetItem, error)
	SeriesAlphabet(ctx context.Context) ([]AlphabetItem, error)
	// ListAuthors/ListSeries/ListGenres — постраничные списки сущностей.
	ListAuthors(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error)
	ListSeries(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error)
	ListGenres(ctx context.Context, page, size int) ([]NamedItem, int64, error)
	// EntityName — название автора/серии/жанра по id (kind: author|series|genre).
	EntityName(ctx context.Context, kind string, id int64) (string, error)
	RandomBook(ctx context.Context) (LibraryBook, error)
	RecentBooks(ctx context.Context, limit int) ([]LibraryBook, error)
	// PrefixIndex — префиксный алфавитный указатель (table,col,length,prefix,lang).
	PrefixIndex(ctx context.Context, table, col string, length int, prefix string, lang int) ([]AlphabetItem, error)
	// BookByPrefixIndex — указатель книг по префиксу.
	BookByPrefixIndex(ctx context.Context, length int, prefix string, lang int) ([]AlphabetItem, error)
	// ListBooksByPrefix — книги по префиксу с фильтром по языку.
	ListBooksByPrefix(ctx context.Context, prefix string, lang, page, size int) (LibraryPage, error)
	// ListAuthorsByPrefix / ListSeriesByPrefix — сущности по префиксу.
	ListAuthorsByPrefix(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error)
	ListSeriesByPrefix(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error)

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
	db       *sql.DB
	kind     config.DBType
	libRoot  string // корень коллекции для чтения файлов книг (напр. /books)
	cacheDir string // каталог кэша обложек (напр. /config/covers)
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

	return &DBStore{
		db:       db,
		kind:     cfg.DBType,
		libRoot:  cfg.LibraryRoot(),
		cacheDir: filepath.Join(cfg.ConfigDir, "covers"),
	}, nil
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
