package admin

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/version"
)

// appVersion возвращает версию приложения.
func appVersion() string { return version.Version }

// goVersion возвращает версию Go, которой собрано приложение.
func goVersion() string { return runtime.Version() }

// itoa — int в строку.
func itoa(n int) string { return strconv.Itoa(n) }

// atoi — строка в int с дефолтом.
func atoi(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// defaultSQLitePath — путь к файлу SQLite по умолчанию (/config/eopds.db).
func defaultSQLitePath(cfg config.Config) string {
	if cfg.DBPath != "" {
		return cfg.DBPath
	}
	return filepath.Join(cfg.ConfigDir, "eopds.db")
}

// loggerAdapter приводит *slog.Logger к интерфейсу migrate.Logger.
type loggerAdapter struct{ l *slog.Logger }

func (a loggerAdapter) Infof(module, format string, args ...any) {
	a.l.Info(module + ": " + fmt.Sprintf(format, args...))
}

func (a loggerAdapter) Debugf(module, format string, args ...any) {
	a.l.Debug(module + ": " + fmt.Sprintf(format, args...))
}

// LoggerAdapter экспортирует адаптер migrate.Logger для внешних пакетов.
func LoggerAdapter(l *slog.Logger) loggerAdapter { return loggerAdapter{l: l} }
