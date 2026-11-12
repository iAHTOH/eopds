# Go-подсистема `eopds-go`

Единый Go-модуль с подкомандами. Стек — чистый stdlib, CGO выключен.

## Структура

```
eopds-go/
├── go.mod
├── cmd/
│   └── eopds/
│       └── main.go          # точка входа: подкоманды scan / admin
├── internal/
│   ├── version/             # версия, git-commit, build-time
│   ├── config/              # чтение ENV (EOPDS_DB_*, EOPDS_*), совместимость
│   ├── store/               # абстракция БД (интерфейс Store + sqlite/pgx)
│   ├── admin/               # HTTP-сервер админки (stdlib, html/template)
│   │   ├── server.go
│   │   ├── handlers_*.go
│   │   ├── templates/       # html/template (дашборд, темы)
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

Параллельный сканер (заменяет Python-сканер при `EOPDS_SCANNER_ENGINE=go`).
Параметры: `--root`, `--workers`, `--zip`, `--exts`, `--level`.
Выдаёт JSON Lines в stdout (контракт `meta`). Подробнее — [scanner.md](scanner.md).

### `eopds admin`

HTTP-сервер админки и API. В разработке слушает отдельный порт
(`--addr`, по умолчанию `:8081`); в едином портале — `:8080`.
Эндпоинты — в [api.md](api.md).

## Сборка

```bash
cd eopds-go
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$(cat ../VERSION)" \
  -o eopds ./cmd/eopds
```

## Версия

Единый источник — `VERSION` в корне репозитория. См. [../versioning.md](../versioning.md).
