-- Миграция 5: расширение профиля пользователя.
-- Все данные профиля хранятся в БД (для бекапов/восстановления).
--
-- display_name    — имя для приветствия (логин username не меняется).
-- avatar          — имя файла аватара в /config/avatars/ (NULL — нет).
-- allow_adult     — разрешён ли показ книг 18+ (правит модератор/админ; в профиле read-only).
--                   Default 1: метки 18+ пока не вводятся, разрешаем по умолчанию.
-- pref_lang       — язык интерфейса по умолчанию ('' — из запроса/сессии).
-- pref_theme      — тема по умолчанию: 'light'/'dark'/''.
-- read_font       — шрифт читалки по умолчанию ('' — системный).
-- read_font_size  — размер шрифта читалки, % (0/100 — по умолчанию).

ALTER TABLE users ADD COLUMN display_name   TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar         TEXT;
ALTER TABLE users ADD COLUMN allow_adult    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN pref_lang      TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN pref_theme     TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN read_font      TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN read_font_size INTEGER NOT NULL DEFAULT 0;

UPDATE app_meta SET schema_version = 5 WHERE id = 1;
