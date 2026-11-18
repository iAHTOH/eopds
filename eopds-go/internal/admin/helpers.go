package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

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

// resolveTheme определяет активную тему: ?theme=<name>, cookie или "dark".
func resolveTheme(r *http.Request) string {
	if t := r.URL.Query().Get("theme"); isKnownTheme(t) {
		return t
	}
	if c, err := r.Cookie("theme"); err == nil && isKnownTheme(c.Value) {
		return c.Value
	}
	return "dark"
}

// resolveLang определяет язык: ?lang=, cookie, Accept-Language, иначе "en".
func resolveLang(r *http.Request) string {
	// ?lang= трактуем как локаль интерфейса только если это известный
	// языковой код (ru/en). Числовые значения (0..9) — это фильтр БД
	// (язык книг), их здесь игнорируем, иначе в подменю «Кириллица»
	// переключался бы язык сайта.
	if l := r.URL.Query().Get("lang"); isUILang(l) {
		return l
	}
	if c, err := r.Cookie("lang"); err == nil && c.Value != "" {
		return c.Value
	}
	// Accept-Language: берём первый тег с точностью до "xx".
	if al := r.Header.Get("Accept-Language"); al != "" {
		first := strings.SplitN(al, ",", 2)[0]
		first = strings.TrimSpace(strings.SplitN(first, ";", 2)[0])
		if i := strings.IndexAny(first, "-_"); i > 0 {
			first = first[:i]
		}
		if first != "" {
			return first
		}
	}
	return "en"
}

// isUILang проверяет, что значение ?lang= — код языка интерфейса (ru/en),
// а не числовой фильтр БД (0..9).
func isUILang(v string) bool {
	switch v {
	case "ru", "en":
		return true
	default:
		return false
	}
}

// langName — человекочитаемое название языка для переключателя.
func langName(code string) string {
	switch code {
	case "ru":
		return "Русский"
	case "en":
		return "English"
	default:
		return code
	}
}

// langCode — двухбуквенный код языка в верхнем регистре (RU, EN).
func langCode(code string) string {
	if i := strings.IndexAny(code, "-_"); i > 0 {
		code = code[:i]
	}
	return strings.ToUpper(code)
}

// roleName — локализованное имя роли (ключ T).
func roleName(role string) string {
	switch role {
	case "admin":
		return "Admin"
	case "moderator":
		return "Moderator"
	case "reader":
		return "Reader"
	default:
		return role
	}
}

// isKnownTheme проверяет, что тема существует (theme-<name>.css).
func isKnownTheme(name string) bool {
	switch name {
	case "dark", "light":
		return true
	default:
		return false
	}
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

// kb переводит байты в килобайты (округление вниз).
func kb(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return n / 1024
}

// trimText обрезает строку до max рун, добавляя многоточие.
func trimText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
