-- Миграция 3: таблица отпечатков прочитанных файлов (инкрементальный скан).
-- Хранит size+mtime каждого успешно обработанного файла/архива, чтобы
-- повторное сканирование пропускало неизменившиеся файлы без парсинга.
-- path — относительный путь файла (книга: rel_dir/filename; архив: rel_dir/name).

CREATE TABLE scan_files (
    path  TEXT   PRIMARY KEY,
    size  BIGINT NOT NULL DEFAULT 0,
    mtime BIGINT NOT NULL DEFAULT 0   -- unix-время в наносекундах (int64)
);

UPDATE app_meta SET schema_version = 3 WHERE id = 1;
