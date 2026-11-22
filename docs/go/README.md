# Go-подсистема `eopds-go`

Единый Go-модуль с подкомандами. Стек — чистый stdlib, CGO выключен.

## Структура

```
eopds-go/
├── go.mod
├── cmd/
│   └── eopds/
│       └── main.go          # точка входа: подкоманды serve/admin/scan
├── internal/
│   ├── version/             # версия, git-commit, build-time
│   ├── i18n/                # translations.json (go:embed), LINGUAS, Translator
│   ├── config/              # чтение ENV (EOPDS_DB_*, EOPDS_*), /config/db.json
│   ├── store/               # абстракция БД (интерфейс Store + sqlite/pgx)
│   ├── migrate/             # встроенные миграции (go:embed) + раннер
│   ├── auth/                # хэширование паролей (PBKDF2-SHA256)
│   ├── admin/               # HTTP-сервер админки + мастер установки
│   │   ├── server.go
│   │   ├── install.go       # мастер первичной установки
│   │   ├── handlers_*.go
│   │   ├── templates/       # html/template (установка, дашборд)
│   │   └── static/          # ванильный CSS/JS (единая тема портала)
│   ├── upgrade/             # проверка версий (eopds.ru / github)
│   ├── forum/               # клиент «последние темы» с кэшем
│   ├── meta/                # контракт обмена сканера (JSON Lines)
│   ├── fb2/                 # парсер FB2
│   ├── walk/                # параллельный обход
│   ├── charset/             # windows-1251 -> UTF-8
│   ├── emit/                # эмиссия JSON Lines
│   └── logx/                # логи
```

## Подкоманды

### `eopds scan`

Параллельный сканер (нативный приёмник в БД — `internal/scansvc`).
Параметры: `--root`, `--workers`, `--zip`, `--exts`, `--level`.
Выдаёт JSON Lines в stdout (контракт `meta`). Подробнее — [scanner.md](scanner.md).

### `eopds admin` / `eopds serve`

HTTP-сервер админки и API. В разработке слушает отдельный порт
(`--addr`/`EOPDS_SERVER_ADDR`, по умолчанию `:8080`).
При первом запуске (пустая/ненастроенная БД) показывает **мастер установки**
(`/install`) — см. [install.md](install.md). Эндпоинты — в [api.md](api.md).

Веб-библиотека (страницы `/library/*`, каркас, указатель) — [library.md](library.md).
Дашборд и журнал событий («Сообщения системы») — [system-events.md](system-events.md).

## Сборка

```bash
cd eopds-go
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$(cat ../VERSION)" \
  -o eopds ./cmd/eopds
```

## Версия

Единый источник — `VERSION` в корне репозитория. См. [../versioning.md](../versioning.md).
