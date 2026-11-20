# HTTP API Go-сервиса (`eopds serve`)

Базовый префикс API: `/api`. Все ответы API — JSON (`application/json`).
Аутентификация для admin-API — в разработке (см. roadmap); публичные
read-only эндпоинты доступны без неё.

## UI-маршруты (HTML)

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/` | главная админки (если не установлено → редирект на `/install`) |
| GET | `/install` | мастер первичной установки ([install.md](install.md)) |
| POST | `/install` | обработка формы установки; успех → `303` на `/` |
| GET | `/healthz` | простой алиас `/api/health` |
| GET | `/library/book/{id}/cover` | обложка книги (image/*, `200`/`404`), кэш `/config/covers` |
| GET | `/library/book/{id}/download` | скачивание файла книги (`?fmt=`, только авторизованным) |
| POST | `/settings/clear-library` | очистка библиотеки (admin); `confirm=yes` → `303` |
| GET | `/library/suggest` | JSON-подсказки живого поиска (`?q=&by=title\|author\|series`) |

## `GET /api/health`

Состояние сервиса (алиас: `GET /healthz`).

```json
{
  "status": "ok",
  "version": "0.47.0",
  "commit": "abc1234",
  "built_at": "2026-01-16T10:00:00Z",
  "schema_version": 3,
  "db": "postgres"
}
```

`status`: `ok` | `degraded` | `error`.
`degraded` — например, несовпадение `schema_version`.

## `GET /api/version`

Версия и сведения о сборке.

```json
{
  "version": "0.47.0",
  "commit": "abc1234",
  "built_at": "2026-01-16T10:00:00Z",
  "go": "go1.23.4"
}
```

## `GET /api/upgrade-check`

Проверка наличия новой версии (кэшируется, таймаут 3с).
Источник — `EOPDS_UPDATE_SOURCE`. Подробнее — [../versioning.md](../versioning.md).

```json
{
  "current": "0.47.0",
  "latest": "0.48.0",
  "update_available": true,
  "url": "https://eopds.ru/topic/5/release-notes",
  "checked_at": "2026-01-16T10:00:00Z"
}
```

## `GET /library/suggest`

Подсказки живого поиска для веб-библиотеки (используется фронтендом строки поиска).

**Параметры запроса:**

| Параметр | Тип | Описание |
|---|---|---|
| `q` | string | строка поиска; короче 2 символов → пустой `items` |
| `by` | string | `title` (по названию, по умолчанию), `author` (по автору), `series` (по серии) |

**Коды ответов:** `200` — всегда (при ошибке БД — `500`).

**Пример:** `GET /library/suggest?q=анна&by=title`

```json
{"items":[
  {"id":11364,"title":"Анна Каренина","authors":["Лев Толстой"],
   "annotation":"«Анна Каренина» — …","format":"fb2"}
]}
```

Аннотация обрезается до 240 символов. Обложка — отдельным запросом
`/library/book/{id}/cover`.

## `GET /library/book/{id}/download`

Скачивание файла книги. **Требует авторизации** (гость → `303` на `/login`).
Ответ — файл (`Content-Disposition: attachment`), не JSON.

**Параметры:**

| Параметр | Тип | Описание |
|---|---|---|
| `id` | path | id книги |
| `fmt` | query | пусто/`<исходный>` — как есть; `fb2+zip` — ZIP «на лету»; прочие форматы (конвертация) → `404` |

**Коды ответов:**

| Код | Значение |
|---|---|
| 200 | файл отдан |
| 303 | гость → редирект на `/login` |
| 404 | книга не найдена, файл отсутствует или формат пока не поддерживается |

**Заголовки ответа:** `Content-Type` по формату (`application/x-fictionbook+xml`,
`application/epub+zip`, `application/zip`, …), `Content-Length` (если известен),
`Content-Disposition: attachment; filename*=UTF-8''<name>`.

**Побочный эффект:** для авторизованного пользователя книга добавляется на его
полку (`bookshelf`, обновляется `last_read_at`).

## `GET /api/forum/latest-topics`

Последние темы форума (кэш, таймаут 3с). Источник — `EOPDS_FORUM_SOURCE`.
Формат источника — [../forum.md](../forum.md).

```json
{
  "topics": [
    {
      "title": "Установка Easy OPDS через docker-compose",
      "url": "https://eopds.ru/topic/4/...",
      "category": "Техподдержка",
      "replies": 0,
      "last_post_at": "2026-01-12T08:30:00Z"
    }
  ]
}
```

При недоступности источника — `200` с пустым массивом или `503`
(поведение уточняется; UI скрывает блок при пустом ответе).

## Коды ответов

| Код | Значение |
|---|---|
| 200 | OK |
| 503 | Источник временно недоступен (для внешних источников) |
| 500 | Внутренняя ошибка |
