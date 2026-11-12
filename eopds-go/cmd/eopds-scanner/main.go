// Command eopds-scanner — параллельный сканер коллекции для EOPDS.
//
// Обходит файловую систему и ZIP-архивы, извлекает метаданные книг
// (пока FB2) и выдаёт их в stdout потоком JSON Lines (см. internal/meta).
// Логи и диагностика идут в stderr (см. internal/logx).
//
// Python-адаптер (opds_catalog/scan_engine_go.py) читает stdout построчно
// и пишет данные в БД через существующую ORM-логику (opdsdb.*).
//
// Пример запуска:
//
//	eopds-scanner --root /library --workers 8 --zip --exts .fb2,.fb2.zip \
//	              --debug --level info
package main

import (
	"flag"
	"os"
	"runtime"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/emit"
	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/walk"
)

func main() {
	var (
		root     string
		workers  int
		zipScan  bool
		extsStr  string
		debug    bool
		levelStr string
	)

	flag.StringVar(&root, "root", "", "Корневой каталог библиотеки (EOPDS_ROOT_LIB)")
	flag.IntVar(&workers, "workers", 0, "Число параллельных воркеров (0 = NumCPU)")
	flag.BoolVar(&zipScan, "zip", true, "Сканировать ZIP-архивы")
	flag.StringVar(&extsStr, "exts", ".fb2", "Расширения книг через запятую (например: .fb2)")
	flag.BoolVar(&debug, "debug", false, "Подробные отладочные логи (эквивалент --level debug)")
	flag.StringVar(&levelStr, "level", "info", "Уровень логов: error|warn|info|debug")
	flag.Parse()

	if root == "" {
		// allow positional root as convenience
		if flag.NArg() > 0 {
			root = flag.Arg(0)
		}
	}
	if root == "" {
		os.Stderr.WriteString("eopds-scanner: --root is required\n")
		os.Exit(2)
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	level := logx.ParseLevel(levelStr)
	if debug {
		level = logx.LevelDebug
	}
	logger := logx.New(level)

	exts := parseExts(extsStr)

	em := emit.New(os.Stdout)
	defer em.Flush()

	opts := walk.Options{
		Root:    root,
		Workers: workers,
		Zip:     zipScan,
		Exts:    exts,
		Debug:   debug,
		Logger:  logger,
		Emitter: em,
	}

	logger.Infof("main", "eopds-scanner starting (version=%s, go=%s)", version, runtime.Version())
	walk.Run(opts)
	if err := em.Flush(); err != nil {
		logger.Errorf("main", "flush error: %v", err)
		os.Exit(1)
	}
}

// version заполняется при сборке через -ldflags "-X main.version=..."
var version = "dev"

func parseExts(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{".fb2"}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}
