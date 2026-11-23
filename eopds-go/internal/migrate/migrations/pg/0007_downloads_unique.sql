-- Миграция 7: одна запись на (пользователь, книга) в downloads.
--
-- Счётчик популярности книги теперь считает УНИКАЛЬНЫХ пользователей
-- (скачавших / прочитавших / получивших по OPDS). Чтобы таблица не раздувалась
-- повторами, вводим частичный уникальный индекс по (book_id, user_id) только
-- для залогиненных пользователей (user_id IS NOT NULL); анонимные строки
-- (user_id IS NULL) не ограничиваем.
--
-- Сначала схлопываем возможные дубли (оставляем самую раннюю запись).

DELETE FROM downloads d
USING downloads keep
WHERE d.book_id = keep.book_id
  AND d.user_id = keep.user_id
  AND d.user_id IS NOT NULL
  AND (keep.downloaded_at < d.downloaded_at
       OR (keep.downloaded_at = d.downloaded_at AND keep.id < d.id));

CREATE UNIQUE INDEX downloads_book_user_uidx
    ON downloads (book_id, user_id)
    WHERE user_id IS NOT NULL;

UPDATE app_meta SET schema_version = 7 WHERE id = 1;
