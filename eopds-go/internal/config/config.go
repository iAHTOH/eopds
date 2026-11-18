// Package config — конфигурация Go-сервисов EOPDS.
//
// Единый источник конфигурации — файл /config/config.json (создаётся мастером
// установки либо редактируется администратором с хоста; каталог /config
// монтируется bind-mount'ом). Файл читаемый (JSON) и удобен для бэкапа.
//
// Он объединяет:
//   - БД (type/host/port/name/user/password/path);
//   - каталоги сканирования (scan_paths; список, может быть несколько);
//   - корневого администратора (username + password_hash);
//   - адрес HTTP-сервера.
//
// Совместимость развёртывания: ENV-переменные (EOPDS_DB_*, EOPDS_SERVER_ADDR,
// EOPDS_CONFIG_DIR, EOPDS_ROOT_LIB) имеют приоритет над файлом. Legacy-файл
// /config/db.json (только БД) также поддерживается как fallback.
//
// Приоритет загрузки: ENV > config.json > db.json (legacy) > значения по умолчанию.
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

// AdminConfig — корневой администратор, задаваемый конфигом.
//
// PasswordHash хранится в формате auth.HashPassword (PBKDF2-SHA256), чтобы
// файл был безопасен для бэкапа. Поле Password (plain) допустимо только для
// ручного заполнения: при старте оно хешируется и переносится в PasswordHash.
type AdminConfig struct {
	Username     string `json:"username"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	Password     string `json:"password,omitempty"`
}

// Config — полная конфигурация сервиса (в памяти).
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
	ConfigDir string   // /config
	ScanPaths []string // каталоги сканирования (может быть несколько)

	// Корневой администратор из конфига (может быть пустым).
	Admin AdminConfig
}

// configFile — структура /config/config.json.
type configFile struct {
	SchemaVersion int          `json:"schema_version,omitempty"`
	Server        serverFile   `json:"server"`
	DB            dbFile       `json:"db"`
	ScanPaths     []string     `json:"scan_paths,omitempty"`
	Admin         *AdminConfig `json:"admin,omitempty"`
}

type serverFile struct {
	Addr string `json:"addr,omitempty"`
}

// dbFile — секция db в config.json (и legacy /config/db.json).
type dbFile struct {
	Type     string `json:"type"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Name     string `json:"name,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Path     string `json:"path,omitempty"`
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
		ScanPaths: nil,
	}
}

// ConfigPath возвращает путь к единому файлу конфигурации.
func ConfigPath(configDir string) string {
	return filepath.Join(configDir, "config.json")
}

// DBConfigPath возвращает путь к legacy-файлу конфигурации БД.
func DBConfigPath(configDir string) string {
	return filepath.Join(configDir, "db.json")
}

// Configured сообщает, задана ли конфигурация (ENV, config.json или db.json).
// Если не задана — сервис должен показать мастер установки.
func Configured(cfg Config) bool {
	if os.Getenv("EOPDS_DB_TYPE") != "" {
		return true
	}
	if _, err := os.Stat(ConfigPath(cfg.ConfigDir)); err == nil {
		return true
	}
	if _, err := os.Stat(DBConfigPath(cfg.ConfigDir)); err == nil {
		return true
	}
	return false
}

// DBConfigured оставлен для совместимости — см. Configured.
func DBConfigured(cfg Config) bool { return Configured(cfg) }

// Load собирает конфигурацию: ENV > config.json > db.json (legacy) > defaults.
func Load() (Config, error) {
	cfg := Default()

	// Базовые пути из ENV (влияют на поиск файла конфига).
	if v := os.Getenv("EOPDS_CONFIG_DIR"); v != "" {
		cfg.ConfigDir = v
	}

	// 1) Единый config.json; 2) legacy db.json.
	var loadErr error
	if _, err := os.Stat(ConfigPath(cfg.ConfigDir)); err == nil {
		if err := cfg.applyConfigFile(); err != nil {
			loadErr = err
		}
	} else if _, err := os.Stat(DBConfigPath(cfg.ConfigDir)); err == nil {
		if err := cfg.applyDBFile(); err != nil {
			loadErr = err
		}
	}

	// 3) ENV поверх (совместимость развёртывания).
	if v := os.Getenv("EOPDS_SERVER_ADDR"); v != "" {
		cfg.Addr = v
	}
	// EOPDS_ROOT_LIB — дефолтный каталог сканирования (если список ещё пуст).
	if v := os.Getenv("EOPDS_ROOT_LIB"); v != "" && len(cfg.ScanPaths) == 0 {
		cfg.ScanPaths = []string{v}
	}
	if os.Getenv("EOPDS_DB_TYPE") != "" {
		cfg.applyEnv()
	}

	if loadErr != nil {
		return cfg, loadErr
	}
	if !Configured(cfg) {
		// Не сконфигурировано — вернём дефолты; сервис покажет инсталлятор.
		return cfg, nil
	}
	return cfg, cfg.validate()
}

// applyConfigFile читает /config/config.json.
func (c *Config) applyConfigFile() error {
	b, err := os.ReadFile(ConfigPath(c.ConfigDir))
	if err != nil {
		return err
	}
	var f configFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("config: parse config.json: %w", err)
	}
	if f.Server.Addr != "" {
		c.Addr = f.Server.Addr
	}
	c.applyDB(f.DB)
	if len(f.ScanPaths) > 0 {
		c.ScanPaths = append([]string(nil), f.ScanPaths...)
	}
	if f.Admin != nil {
		c.Admin = *f.Admin
	}
	return nil
}

// applyDB заполняет поля БД из секции db.
func (c *Config) applyDB(f dbFile) {
	if t := DBType(strings.ToLower(f.Type)); t != "" {
		c.DBType = t
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
	if f.User != "" {
		c.DBUser = f.User
	}
	c.DBPassword = f.Password
	c.DBPath = f.Path
}

// applyEnv заполняет поля БД из ENV (приоритетнее файла).
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

// applyDBFile читает legacy /config/db.json (только БД).
func (c *Config) applyDBFile() error {
	b, err := os.ReadFile(DBConfigPath(c.ConfigDir))
	if err != nil {
		return err
	}
	var f dbFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("config: parse db.json: %w", err)
	}
	c.applyDB(f)
	return nil
}

// SaveConfigFile сохраняет единый конфиг в /config/config.json.
func SaveConfigFile(configDir string, cfg Config) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", configDir, err)
	}
	f := configFile{
		SchemaVersion: 2,
		Server:        serverFile{Addr: cfg.Addr},
		ScanPaths:     cfg.ScanPaths,
		DB: dbFile{
			Type:     string(cfg.DBType),
			Host:     cfg.DBHost,
			Port:     cfg.DBPort,
			Name:     cfg.DBName,
			User:     cfg.DBUser,
			Password: cfg.DBPassword,
			Path:     cfg.DBPath,
		},
	}
	if cfg.Admin.Username != "" {
		a := cfg.Admin
		f.Admin = &a
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := ConfigPath(configDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// SaveDBFile — совместимость: сохраняет legacy /config/db.json без admin/scan.
// Не рекомендуется к использованию; оставлен для внешних вызовов.
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
		return fmt.Errorf("config: write %s: %w", path, err)
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

// LibraryRoot возвращает корневой каталог коллекции (первый каталог
// сканирования). Используется для чтения файлов книг (обложки и т.п.),
// где пути в БД хранятся относительно этого корня.
func (c Config) LibraryRoot() string {
	if len(c.ScanPaths) > 0 && c.ScanPaths[0] != "" {
		return c.ScanPaths[0]
	}
	if v := os.Getenv("EOPDS_ROOT_LIB"); v != "" {
		return v
	}
	return "/books"
}
