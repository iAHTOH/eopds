# HTTP API Go-сервиса (`eopds admin`)

Базовый префикс: `/api`. Все ответы — JSON (`application/json`).
Аутентификация для admin-API — в разработке (см. roadmap); публичные
read-only эндпоинты доступны без неё.

## `GET /api/health`

Состояние сервиса.

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
