-- Миграция 8: исходящая почта (SMTP), журнал отправок, токены подтверждения.
-- См. pg/0008_mail_smtp.sql для описания.
-- Время — INTEGER unix ms (как system_events): одинаково для PG/SQLite.

CREATE TABLE mail_smtp (
    id             INTEGER  PRIMARY KEY CHECK (id = 1),
    enabled        INTEGER  NOT NULL DEFAULT 0,
    host           TEXT     NOT NULL DEFAULT '',
    port           INTEGER  NOT NULL DEFAULT 587,
    security       TEXT     NOT NULL DEFAULT 'starttls'
                   CHECK (security IN ('smtps', 'starttls', 'none')),
    username       TEXT     NOT NULL DEFAULT '',
    password       TEXT     NOT NULL DEFAULT '',
    from_addr      TEXT     NOT NULL DEFAULT '',
    from_name      TEXT     NOT NULL DEFAULT '',
    reply_to       TEXT     NOT NULL DEFAULT '',
    verified_at    INTEGER  NOT NULL DEFAULT 0,
    verify_message TEXT     NOT NULL DEFAULT '',
    updated_at     TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_by_id  INTEGER  REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE mail_sent (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,
    recipient  TEXT    NOT NULL,
    subject    TEXT    NOT NULL DEFAULT '',
    template   TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL DEFAULT 'queued'
               CHECK (status IN ('queued', 'sent', 'error')),
    error      TEXT    NOT NULL DEFAULT '',
    message_id TEXT    NOT NULL DEFAULT '',
    lang       TEXT    NOT NULL DEFAULT '',
    user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX mail_sent_ts_idx        ON mail_sent (ts DESC);
CREATE INDEX mail_sent_recipient_idx ON mail_sent (recipient);

CREATE TABLE email_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      TEXT    NOT NULL,
    kind       TEXT    NOT NULL
               CHECK (kind IN ('reset_password', 'verify_email')),
    token_hash TEXT    NOT NULL,
    ts         INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX email_tokens_hash_uidx ON email_tokens (token_hash);
CREATE INDEX email_tokens_user_idx ON email_tokens (user_id, kind);

UPDATE app_meta SET schema_version = 8 WHERE id = 1;
