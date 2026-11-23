-- Миграция 7: одна запись на (пользователь, книга) в downloads (SQLite).
-- См. pg/0007_downloads_unique.sql для описания.

DELETE FROM downloads
WHERE user_id IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM downloads AS keep
    WHERE keep.book_id = downloads.book_id
      AND keep.user_id = downloads.user_id
      AND (keep.downloaded_at < downloads.downloaded_at
           OR (keep.downloaded_at = downloads.downloaded_at AND keep.id < downloads.id))
  );

CREATE UNIQUE INDEX downloads_book_user_uidx
    ON downloads (book_id, user_id)
    WHERE user_id IS NOT NULL;

UPDATE app_meta SET schema_version = 7 WHERE id = 1;
