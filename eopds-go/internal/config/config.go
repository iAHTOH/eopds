// Package config — конфигурация Go-сервисов EOPDS из ENV-переменных.
//
// Сохраняет контракт развёртывания (docs/architecture.md, docs/deploy):
//   - EOPDS_DB_TYPE (postgres|sqlite), EOPDS_DB_HOST/PORT/NAME/USER/PASSWORD
//   - EOPDS_DB_PATH (sqlite), порт 8080, каталоги /config и /books.
//
// Конфигурация БД может также храниться в файле /config/db.json, который
// создаёт мастер установки (инсталлятор), если ENV не заданы. Приоритет:
// ENV > db.json > значения по умолчанию.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DBType — тип СУБД.
type DBType string

const (
	DBPostgres DBType = "postgres"
	DBSQLite   DBType = "sqlite"
)

// Config — полная конфигурация сервиса.
type Config struct {
	// БД.
	DBType     DBType
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBPath     string // для SQLite

	// Сервер.
	Addr string // например ":8080"

	// Каталоги.
	ConfigDir string // /config
	BooksDir  string // /books
}

// Default — значения по умолчанию (соответствуют контракту развёртывания).
func Default() Config {
	return Config{
		DBType:    DBPostgres,
		DBHost:    "localhost",
		DBPort:    5432,
		DBName:    "eopds",
		Addr:      ":8080",
		ConfigDir: "/config",
		BooksDir:  "/books",
	}
}

// dbFile — структура /config/db.json, создаваемая инсталлятором.
type dbFile struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Path     string `json:"path"`
}

// DBConfigPath возвращает путь к файлу конфигурации БД.
func DBConfigPath(configDir string) string {
	return filepath.Join(configDir, "db.json")
}

// DBConfigured сообщает, задана ли конфигурация БД (через ENV или db.json).
// Если не задана — сервис должен показать мастер установки.
func DBConfigured(cfg Config) bool {
	if os.Getenv("EOPDS_DB_TYPE") != "" {
		return true
	}
	_, err := os.Stat(DBConfigPath(cfg.ConfigDir))
	return err == nil
}

// Load собирает конфигурацию из ENV с fallback на /config/db.json.
func Load() (Config, error) {
	cfg := Default()

	if v := os.Getenv("EOPDS_SERVER_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("EOPDS_CONFIG_DIR"); v != "" {
		cfg.ConfigDir = v
	}
	if v := os.Getenv("EOPDS_ROOT_LIB"); v != "" {
		cfg.BooksDir = v
	}

	// ENV приоритетнее.
	envType := os.Getenv("EOPDS_DB_TYPE")
	if envType != "" {
		cfg.applyEnv()
		return cfg, cfg.validate()
	}

	// Fallback: файл, созданный инсталлятором.
	if DBConfigured(cfg) {
		if err := cfg.applyDBFile(); err != nil {
			return cfg, err
		}
		return cfg, cfg.validate()
	}

	// Не сконфигурировано — вернём дефолты; сервис покажет инсталлятор.
	return cfg, nil
}

// applyEnv заполняет поля БД из ENV.
func (c *Config) applyEnv() {
	c.DBType = DBType(strings.ToLower(os.Getenv("EOPDS_DB_TYPE")))
	if v := os.Getenv("EOPDS_DB_HOST"); v != "" {
		c.DBHost = v
	}
	if v := os.Getenv("EOPDS_DB_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.DBPort = p
		}
	}
	if v := os.Getenv("EOPDS_DB_NAME"); v != "" {
		c.DBName = v
	}
	if v := os.Getenv("EOPDS_DB_USER"); v != "" {
		c.DBUser = v
	}
	if v := os.Getenv("EOPDS_DB_PASSWORD"); v != "" {
		c.DBPassword = v
	}
	if v := os.Getenv("EOPDS_DB_PATH"); v != "" {
		c.DBPath = v
	}
}

// applyDBFile читает /config/db.json.
func (c *Config) applyDBFile() error {
	b, err := os.ReadFile(DBConfigPath(c.ConfigDir))
	if err != nil {
		return err
	}
	var f dbFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("config: parse db.json: %w", err)
	}
	c.DBType = DBType(strings.ToLower(f.Type))
	if c.DBType == "" {
		c.DBType = DBPostgres
	}
	if f.Host != "" {
		c.DBHost = f.Host
	}
	if f.Port != 0 {
		c.DBPort = f.Port
	}
	if f.Name != "" {
		c.DBName = f.Name
	}
	c.DBUser = f.User
	c.DBPassword = f.Password
	c.DBPath = f.Path
	return nil
}

// SaveDBFile сохраняет конфигурацию БД в /config/db.json (вызывается установщиком).
func SaveDBFile(configDir string, cfg Config) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", configDir, err)
	}
	f := dbFile{
		Type:     string(cfg.DBType),
		Host:     cfg.DBHost,
		Port:     cfg.DBPort,
		Name:     cfg.DBName,
		User:     cfg.DBUser,
		Password: cfg.DBPassword,
		Path:     cfg.DBPath,
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := DBConfigPath(configDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// validate проверяет обязательные поля для выбранного типа БД.
func (c Config) validate() error {
	switch c.DBType {
	case DBPostgres:
		if c.DBHost == "" || c.DBName == "" || c.DBUser == "" {
			return fmt.Errorf("config: postgres requires host, name and user")
		}
		if c.DBPort == 0 {
			return fmt.Errorf("config: postgres requires port")
		}
	case DBSQLite:
		if c.DBPath == "" {
			return fmt.Errorf("config: sqlite requires path")
		}
	case "":
		return fmt.Errorf("config: EOPDS_DB_TYPE is empty")
	default:
		return fmt.Errorf("config: unsupported DB type %q", c.DBType)
	}
	return nil
}

// PostgresDSN строит DSN для jackc/pgx (URL-формат).
func (c Config) PostgresDSN() string {
	hostPort := net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort))
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		c.DBUser, c.DBPassword, hostPort, c.DBName)
}

// SQLiteDSN строит DSN для modernc.org/sqlite (файл).
// WAL и busy_timeout — для конкурентного доступа приложения и установки.
func (c Config) SQLiteDSN() string {
	if c.DBPath == "file::memory:" || strings.HasPrefix(c.DBPath, "file:") {
		return c.DBPath
	}
	return "file:" + c.DBPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
}
