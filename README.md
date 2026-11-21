# Easy OPDS — домашняя OPDS-библиотека

**Version 0.49.0** | [Инструкция на русском](README_RUS.md)

![Screenshot](Screenshot.webp)

OPDS-каталог для домашней коллекции электронных книг: сканер библиотеки,
веб-интерфейс и админ-панель. Написан полностью на **Go** (единый бинарник,
stdlib, без CGO). Python/Django-реализация удалена.

---

## Возможности

- **Go-сканер коллекции**: параллельный обход, FB2 и архивы ZIP с FB2,
  windows-1251, ранний выход после метаданных; нативная запись в БД (`scansvc`).
- **Веб-библиотека** (`/library/*`): каталоги/папки, списки книг, карточки книг,
  авторы, серии, жанры, алфавитный указатель, поиск с автоподсказками.
- **Админка**: дашборд со статистикой, страница сканирования, очистка библиотеки,
  настройки каталогов сканирования.
- **Установка через веб** (`/install`): мастер настройки БД и корневого админа.
- **HTTP API**: `/api/health`, `/api/version`, `/api/upgrade-check`,
  `/api/forum/latest-topics`.
- **Темы** (светлая/тёмная) и **локализация** ru/en (через `translations.json`).
- **БД**: PostgreSQL (рекомендуется) или SQLite.

> Планируется (заново на Go): онлайн-читалка, конвертация FB2→EPUB/MOBI,
> публичный OPDS-портал, телеграм-бот. См. [docs/roadmap.md](docs/roadmap.md).

---

## Быстрый старт (Docker)

```bash
# 1) Скопируйте пример переменных окружения
cp .env.example .env

# 2) Укажите путь к коллекции книг в docker-compose.go.yml (том /books):ro
#    (по умолчанию смонтирован /run/media/ahtoh/SSD2/e-book)

# 3) Запуск
docker compose -f docker-compose.go.yml up --build -d
```

Откройте **http://localhost:8080/** — запустится мастер установки:
укажите параметры БД (по умолчанию совпадают с `.env`), создайте корневого
администратора и задайте каталоги сканирования.

Вся конфигурация хранится в `./config/config.json` (монтируется в контейнер),
её можно бэкапить и править с хоста; после правок перезапустите контейнер.

---

## Сборка без Docker

```bash
cd eopds-go
go build -o eopds ./cmd/eopds

# Развёртывание (PostgreSQL)
EOPDS_CONFIG_DIR=./config EOPDS_ROOT_LIB=/path/to/books \
EOPDS_DB_TYPE=postgres EOPDS_DB_HOST=localhost EOPDS_DB_NAME=eopds \
EOPDS_DB_USER=eopds EOPDS_DB_PASSWORD=secret \
./eopds serve
```

Для SQLite: `EOPDS_DB_TYPE=sqlite EOPDS_DB_PATH=/path/to/eopds.db`.

---

## Контракт развёртывания (ENV)

| Переменная | Назначение |
|---|---|
| `EOPDS_CONFIG_DIR` | каталог конфига (по умолчанию `/config`) |
| `EOPDS_ROOT_LIB` | путь к библиотеке (по умолчанию `/books`) |
| `EOPDS_SERVER_ADDR` | адрес сервера (по умолчанию `:8080`) |
| `EOPDS_DB_TYPE` | `postgres` или `sqlite` |
| `EOPDS_DB_HOST` / `EOPDS_DB_PORT` | хост/порт PostgreSQL |
| `EOPDS_DB_NAME` / `EOPDS_DB_USER` / `EOPDS_DB_PASSWORD` | доступ к БД |
| `EOPDS_DB_PATH` | путь к файлу SQLite |

ENV имеют приоритет над `config.json`. Подробнее — [docs/deploy/configuration.md](docs/deploy/configuration.md).

---

## Документация

Полный индекс — [docs/README.md](docs/README.md). Ключевое:

- [docs/architecture.md](docs/architecture.md) — архитектура.
- [docs/go/scanner.md](docs/go/scanner.md) — как устроен сканер.
- [docs/go/library.md](docs/go/library.md) — веб-библиотека.
- [docs/go/install.md](docs/go/install.md) — установка.
- [docs/go/api.md](docs/go/api.md) — HTTP API.
- [docs/roadmap.md](docs/roadmap.md) — план развития.

---

## Поддержка

- **Форум**: [https://eopds.ru](https://eopds.ru)
- **GitHub**: [https://github.com/iAHTOH/eopds](https://github.com/iAHTOH/eopds)
