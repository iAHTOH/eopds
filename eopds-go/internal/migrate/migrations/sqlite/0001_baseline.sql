-- Миграция 1: базовая схема ядра (SQLite).
-- Чистая установка. См. docs/db-schema.md.
--
-- Типы SQLite динамические: INTEGER (rowid-идентификаторы), TEXT (время в
-- RFC3339 UTC), INTEGER для флагов/размеров. Соответствие PG-схеме — по смыслу.

PRAGMA foreign_keys = ON;

CREATE TABLE app_meta (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    schema_version INTEGER NOT NULL,
    app_version    TEXT    NOT NULL DEFAULT '',
    installed_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    last_scan_at   TEXT
);
INSERT INTO app_meta (id, schema_version) VALUES (1, 1);

CREATE TABLE schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    description TEXT    NOT NULL DEFAULT ''
);
INSERT INTO schema_migrations (version, description) VALUES (1, 'baseline schema');

CREATE TABLE counters (
    name       TEXT    PRIMARY KEY,
    value      INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE scan_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    finished_at   TEXT,
    status        TEXT    NOT NULL DEFAULT 'running',
    books_added   INTEGER NOT NULL DEFAULT 0,
    books_removed INTEGER NOT NULL DEFAULT 0,
    books_skipped INTEGER NOT NULL DEFAULT 0,
    error         TEXT
);

CREATE TABLE catalogs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_id  INTEGER REFERENCES catalogs(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    path       TEXT    NOT NULL,
    kind       INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    mtime      TEXT
);
CREATE INDEX catalogs_parent_name_idx ON catalogs (parent_id, name);
CREATE UNIQUE INDEX catalogs_path_uidx ON catalogs (path);

CREATE TABLE books (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    catalog_id     INTEGER NOT NULL REFERENCES catalogs(id) ON DELETE CASCADE,
    filename       TEXT    NOT NULL,
    path           TEXT    NOT NULL,
    format         TEXT    NOT NULL DEFAULT '',
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    title          TEXT    NOT NULL DEFAULT '',
    search_title   TEXT    NOT NULL DEFAULT '',
    original_title TEXT,
    annotation     TEXT    NOT NULL DEFAULT '',
    isbn           TEXT,
    publisher      TEXT,
    publish_year   INTEGER,
    docdate        TEXT    NOT NULL DEFAULT '',
    lang           TEXT    NOT NULL DEFAULT '',
    lang_code      INTEGER NOT NULL DEFAULT 9,
    kind           INTEGER NOT NULL DEFAULT 0,
    avail          INTEGER NOT NULL DEFAULT 2,
    source         TEXT,
    external_id    TEXT,
    added_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX books_path_filename_uidx ON books (path, filename);
CREATE INDEX books_search_title_idx ON books (search_title);
CREATE INDEX books_lang_title_idx   ON books (lang_code, search_title);
CREATE INDEX books_avail_idx        ON books (avail);
CREATE INDEX books_catalog_idx      ON books (catalog_id);

CREATE TABLE authors (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    full_name        TEXT    NOT NULL,
    search_full_name TEXT    NOT NULL DEFAULT '',
    lang_code        INTEGER NOT NULL DEFAULT 9
);
CREATE UNIQUE INDEX authors_full_name_uidx ON authors (full_name);
CREATE INDEX authors_search_name_idx ON authors (search_full_name);

CREATE TABLE book_authors (
    book_id   INTEGER NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, author_id)
);
CREATE INDEX book_authors_author_idx ON book_authors (author_id, book_id);

CREATE TABLE genres (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT NOT NULL,
    section    TEXT NOT NULL DEFAULT '',
    subsection TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX genres_code_uidx ON genres (code);

CREATE TABLE book_genres (
    book_id  INTEGER NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    genre_id INTEGER NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, genre_id)
);
CREATE INDEX book_genres_genre_idx ON book_genres (genre_id, book_id);

CREATE TABLE series (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    title        TEXT    NOT NULL,
    search_title TEXT    NOT NULL DEFAULT '',
    lang_code    INTEGER NOT NULL DEFAULT 9
);
CREATE UNIQUE INDEX series_title_uidx ON series (title);

CREATE TABLE book_series (
    book_id   INTEGER NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    seq_no    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, series_id)
);
CREATE INDEX book_series_series_idx ON book_series (series_id, seq_no);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL,
    email         TEXT,
    password_hash TEXT    NOT NULL DEFAULT '',
    role          TEXT    NOT NULL DEFAULT 'reader'
                  CHECK (role IN ('admin', 'moderator', 'reader')),
    is_root       INTEGER NOT NULL DEFAULT 0,
    is_active     INTEGER NOT NULL DEFAULT 1,
    notify_new_books    INTEGER NOT NULL DEFAULT 0,
    notify_new_versions INTEGER NOT NULL DEFAULT 0,
    messenger_kind TEXT,
    messenger_id   TEXT,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX users_username_uidx ON users (username);
-- Ровно один root-админ.
CREATE UNIQUE INDEX users_root_uidx ON users (is_root) WHERE is_root = 1;

CREATE TABLE invites (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'reader'
                  CHECK (role IN ('admin', 'moderator', 'reader')),
    token         TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    expires_at    TEXT,
    accepted_at   TEXT,
    created_by_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX invites_token_uidx ON invites (token);

CREATE TABLE registration_requests (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT    NOT NULL,
    message       TEXT,
    status        TEXT    NOT NULL DEFAULT 'new'
                  CHECK (status IN ('new', 'approved', 'rejected')),
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    handled_at    TEXT,
    handled_by_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX registration_requests_status_idx ON registration_requests (status, created_at);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    actor_name  TEXT    NOT NULL DEFAULT '',
    action      TEXT    NOT NULL,
    target_type TEXT    NOT NULL,
    target_id   TEXT,
    details     TEXT,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at);
CREATE INDEX audit_log_target_idx  ON audit_log (target_type, target_id);

CREATE TABLE settings (
    key           TEXT    PRIMARY KEY,
    value         TEXT    NOT NULL DEFAULT '',
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_by_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE sessions (
    id         TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    expires_at TEXT    NOT NULL,
    data       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE bookshelf (
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id      INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    added_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    last_read_at TEXT,
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX bookshelf_user_read_idx ON bookshelf (user_id, last_read_at);

CREATE TABLE bookmarks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position   TEXT    NOT NULL DEFAULT '',
    note       TEXT,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX bookmarks_user_book_idx ON bookmarks (user_id, book_id);

CREATE TABLE annotations_cache (
    book_id    INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL DEFAULT '',
    fetched_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE book_covers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    kind       INTEGER NOT NULL DEFAULT 1,
    source     TEXT    NOT NULL DEFAULT 'extracted',
    format     TEXT    NOT NULL DEFAULT 'jpeg',
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    file_path  TEXT    NOT NULL DEFAULT '',
    fetched_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX book_covers_book_kind_uidx ON book_covers (book_id, kind);

CREATE TABLE downloads (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id       INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    user_id       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    downloaded_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    format        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX downloads_downloaded_at_idx ON downloads (downloaded_at);
CREATE INDEX downloads_book_idx ON downloads (book_id);
