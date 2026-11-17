-- Миграция 2: бэкфилл books.lang_code по первому значимому символу search_title.
-- Коды: 1 — кириллица, 2 — латиница, 3 — цифра, 9 — прочее.
-- SQLite: используем substr + GLOB (диапазоны юникода для кириллицы не работают в GLOB,
-- поэтому кириллица определяется «не ASCII и не цифра» через набор диапазонов cp1251-UNICODE).

UPDATE books
SET lang_code = CASE
    WHEN substr(search_title, 1, 1) BETWEEN 'А' AND 'я' THEN 1
    WHEN substr(search_title, 1, 1) IN ('Ё', 'ё')        THEN 1
    WHEN (substr(search_title, 1, 1) BETWEEN 'A' AND 'Z')
      OR (substr(search_title, 1, 1) BETWEEN 'a' AND 'z') THEN 2
    WHEN substr(search_title, 1, 1) BETWEEN '0' AND '9'  THEN 3
    ELSE 9
END
WHERE search_title <> '';

UPDATE app_meta SET schema_version = 2 WHERE id = 1;
