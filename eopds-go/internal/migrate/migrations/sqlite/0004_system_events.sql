-- Миграция 4: сообщения системы (журнал событий для дашборда).
-- Отдельно от audit_log: это операционные сообщения (скачивания, входы,
-- ошибки сканирования, действия над пользователями), отображаемые в админке
-- и с ограниченной ёмкостью (ротация по settings-ключу events.max_rows).
--
-- Текст события НЕ хранится готовой строкой: category (код) + args (JSON) +
-- actor/actor_role позволяют вывести сообщение на языке пользователя (i18n).
-- ts — unix-время в миллисекундах (одинаково для PG/SQLite, удобно сортировать).

CREATE TABLE system_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER     NOT NULL,                 -- unix ms
    level      TEXT        NOT NULL DEFAULT 'info',  -- info | warn | error
    category   TEXT        NOT NULL,                 -- scan.error, book.download, ...
    actor      TEXT        NOT NULL DEFAULT '',      -- имя пользователя ('' — система)
    actor_role TEXT        NOT NULL DEFAULT '',      -- admin | moderator | reader | ''
    args       TEXT        NOT NULL DEFAULT '{}',    -- JSON с подстановками
    created_at TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX system_events_ts_idx ON system_events (ts);

UPDATE app_meta SET schema_version = 4 WHERE id = 1;
