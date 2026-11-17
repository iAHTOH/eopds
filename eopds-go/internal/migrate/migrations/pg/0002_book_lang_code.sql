-- Миграция 2: бэкфилл books.lang_code по первому значимому символу search_title.
-- Коды: 1 — кириллица, 2 — латиница, 3 — цифра, 9 — прочее.
-- Цель: включить фильтры «кириллица/латиница/цифры» в веб-интерфейсе.

UPDATE books
SET lang_code = CASE
    WHEN left(search_title, 1) ~ '^[А-Яа-яЁё]$' THEN 1
    WHEN left(search_title, 1) ~ '^[A-Za-z]$'      THEN 2
    WHEN left(search_title, 1) ~ '^[0-9]$'         THEN 3
    ELSE 9
END
WHERE search_title <> '';

UPDATE app_meta SET schema_version = 2 WHERE id = 1;
