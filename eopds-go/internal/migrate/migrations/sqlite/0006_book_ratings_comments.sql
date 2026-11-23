-- Миграция 6: оценки и отзывы пользователей к книгам (SQLite).
-- См. pg/0006_book_ratings_comments.sql для описания.
-- Время — TEXT в ISO-8601 (как в baseline: strftime '%Y-%m-%dT%H:%M:%SZ').

CREATE TABLE book_ratings (
    book_id    INTEGER  NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    user_id    INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    score      INTEGER  NOT NULL CHECK (score >= 0 AND score <= 10),
    created_at TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY (book_id, user_id)
);
CREATE INDEX book_ratings_user_idx ON book_ratings (user_id);

CREATE TABLE book_comments (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    book_id    INTEGER  NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    user_id    INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT     NOT NULL,
    created_at TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX book_comments_book_idx ON book_comments (book_id, created_at DESC);
CREATE INDEX book_comments_user_idx ON book_comments (user_id);

UPDATE app_meta SET schema_version = 6 WHERE id = 1;
