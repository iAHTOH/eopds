# Сообщения системы (журнал событий)

Журнал операционных событий для дашборда: скачивания книг, входы/выходы,
ошибки сканирования, изменения настроек/библиотеки, действия администратора.
Отображается блоком **«Сообщения системы»** на `/dashboard`.

## Доступ

Блок и скачивание журнала видят **admin (root)** и **moderator**.
Остальные роли блок не видят; прямой запрос к скачиванию → `403`.

## Хранение

Таблица `system_events` (миграция `0004_system_events.sql`, см.
[../db-schema.md](../db-schema.md)). Поля:

| Поле | Назначение |
|---|---|
| `id` | PK |
| `ts` | время события, unix **миллисекунды** |
| `level` | `info` / `warn` / `error` |
| `category` | код события (см. таблицу категорий), без перевода |
| `actor` | имя пользователя-инициатора (`''` — система) |
| `actor_role` | `admin` / `moderator` / `reader` / `''` |
| `args` | JSON с подстановками (`{"book":"…","path":"…","error":"…"}`) |
| `created_at` | время вставки (служебное) |

Текст события **не хранится** готовой строкой: `category` + `args` +
`actor`/`actor_role` выводятся на языке пользователя (i18n). Это сохраняет
мультиязычность.

### Ротация и лимиты

- Ёмкость журнала — настройка **`events.max_rows`** (ключ в `settings`,
  по умолчанию **1000**). При вставке более старые события удаляются
  (`DELETE … NOT IN (SELECT id … ORDER BY ts DESC LIMIT events.max_rows)`).
- В дашборде отображаются последние **100** событий (новые сверху).
- Скачиваемый `.log` содержит до `events.max_rows` строк (в аналитических
  целях).

## Категории событий

| category | level | args | Смысл |
|---|---|---|---|
| `scan.start` | info | — | запуск сканирования |
| `scan.end` | info/warn | `created`, `updated`, `errors`, `elapsed` | итог сканирования (warn, если были ошибки) |
| `scan.error` | error | `path`, `error` | ошибка чтения/разбора файла (полный путь, для архива — `архив.zip/файл`) |
| `book.download` | info | `book` | скачивание книги |
| `auth.login` | info | — | успешный вход |
| `auth.login_failed` | warn | `user` | неудачная попытка входа |
| `auth.logout` | info | — | выход |
| `settings.scan_dirs` | info | `count` | изменение каталогов сканирования |
| `lib.clear` | warn | `books` | очистка библиотеки |

Каждая категория имеет сообщение в `internal/i18n/translations.json` под
ключом `ev.<category>` с подстановками `{book}`, `{path}`, `{error}`,
`{created}`, `{updated}`, `{errors}`, `{elapsed}`, `{count}`, `{books}`,
`{user}`. Роль и имя инициатора оформляются в шаблоне отдельно (с мягкой
подсветкой роли).

## Скачивание

`GET /dashboard/events/download?lang=<ru|en>` — отдаёт `.log`
(`text/plain`), одна строка на событие:

```
2026-11-22 16:43:24 [инфо] администратор admin — скачал книгу «Маугли»
2026-11-22 16:45:00 [ошибка] Ошибка сканирования в dir/book.zip/bad.fb2: not well-formed
```

Строки — на языке пользователя (`lang`). Описание эндпоинта —
[api.md](api.md#get-dashboardeventsdownload).

## Внутренняя реализация

- `internal/store/system_events.go` — `AddSystemEvent` (с ротацией),
  `SystemEvents`, `CountSystemEvents`, `ClearSystemEvents`.
- `internal/store/downloads.go` — `AddDownload` (таблица `downloads`,
  аналитика скачиваний).
- `internal/admin/events.go` — `logEvent` / `logEventUser`, чтение
  `events.max_rows`; события пишутся с отвязанным от запроса контекстом
  (`context.WithoutCancel`), чтобы пережить завершение ответа.
- `internal/admin/events_format.go` — `eventViews`/`formatEvent`:
  подстановка `args` в переведённый шаблон.
- `internal/admin/events_handler.go` — скачивание `.log`.
- `internal/admin/events.go` (`canViewEvents`) — проверка доступа.
- Ошибки сканирования — через `scansvc.Options.OnError` (см.
  [scanner.md](scanner.md)).
