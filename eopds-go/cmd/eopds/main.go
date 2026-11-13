// Command eopds — единая точка входа Go-ядра EOPDS.
//
// Подкоманды:
//
//	eopds serve   — HTTP-сервер (админка + будущий портал/OPDS)
//	eopds admin   — то же, что serve (синоним на переходный период)
//	eopds version — печать версии
//
// Контракт развёртывания: ENV EOPDS_DB_*, порт 8080, каталоги /config и /books
// (см. docs/deploy/configuration.md). Если БД не сконфигурирована/недоступна —
// сервер показывает мастер установки.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/admin"
	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
	"github.com/iAHTOH/eopds/eopds-go/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "eopds:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(version.String())
		return nil
	case "serve", "admin":
		return serve()
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("неизвестная подкоманда %q", cmd)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Использование: eopds <команда>

Команды:
  serve    HTTP-сервер (админка + установка)
  admin    синоним serve
  version  показать версию`)
}

// serve запускает HTTP-сервер.
func serve() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load()
	if err != nil {
		// Ошибка конфигурации не фатальна: покажем установщик.
		log.Warn("конфигурация", "err", err)
	}

	srv, err := admin.New(cfg, log)
	if err != nil {
		return err
	}

	// Пытаемся подключиться к БД (если сконфигурирована). Ошибка подключения
	// не фатальна: сервер поднимется и покажет мастер установки/ошибку.
	ctx := context.Background()
	if st, err := store.Open(ctx, cfg, admin.LoggerAdapter(log)); err == nil {
		srv.SetStore(st)
		log.Info("БД подключена", "type", cfg.DBType, "version", version.String())
		installed, _ := st.IsInstalled(ctx)
		if !installed {
			log.Info("система не установлена — требуется мастер установки")
		}
	} else {
		log.Info("БД не подключена, откроется мастер установки", "reason", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP-сервер запущен", "addr", cfg.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Ожидание сигнала завершения или ошибки сервера.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-sig:
		log.Info("остановка по сигналу")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
