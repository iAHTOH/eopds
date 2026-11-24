package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMailSettingsRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// До сохранения — значения по умолчанию без ошибки.
	def, err := st.GetMailSettings(ctx)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def.Enabled || def.Host != "" || def.Port != 587 || def.Security != "starttls" {
		t.Fatalf("unexpected defaults: %+v", def)
	}

	uid := createUser(t, st, "mailadmin")
	want := MailSettings{
		Enabled:    true,
		Host:       "smtp.example.com",
		Port:       465,
		Security:   "smtps",
		Username:   "mailer",
		Password:   "secret",
		FromAddr:   "lib@example.com",
		FromName:   "Библиотека",
		ReplyTo:    "support@example.com",
		VerifiedAt: time.Now().UnixMilli(),
		VerifyMsg:  "smtp.example.com:465, SMTPS",
	}
	if err := st.SaveMailSettings(ctx, want, uid); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.GetMailSettings(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Enabled || got.Host != want.Host || got.Port != want.Port ||
		got.Security != want.Security || got.Username != want.Username ||
		got.Password != want.Password || got.FromAddr != want.FromAddr ||
		got.FromName != want.FromName || got.ReplyTo != want.ReplyTo ||
		got.VerifiedAt != want.VerifiedAt || got.VerifyMsg != want.VerifyMsg {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.UpdatedAt == "" {
		t.Fatal("updated_at must be set")
	}

	// Повторное сохранение — обновление той же строки.
	want.Enabled = false
	want.Host = "smtp2.example.com"
	if err := st.SaveMailSettings(ctx, want, uid); err != nil {
		t.Fatalf("save #2: %v", err)
	}
	got, _ = st.GetMailSettings(ctx)
	if got.Enabled || got.Host != "smtp2.example.com" {
		t.Fatalf("second save not applied: %+v", got)
	}
}

func TestMailSentLog(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	uid := createUser(t, st, "loguser")

	id, err := st.AddMailSent(ctx, MailSent{
		Recipient: "r@example.com", Subject: "Тема", Template: "test",
		Status: "queued", Lang: "ru", UserID: uid,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id <= 0 {
		t.Fatalf("bad id %d", id)
	}
	if err := st.UpdateMailSent(ctx, id, "sent", "", "msg-1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, err := st.ListMailSent(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 record, got %d", len(list))
	}
	m := list[0]
	if m.Status != "sent" || m.MessageID != "msg-1" || m.UserID != uid || m.Recipient != "r@example.com" {
		t.Fatalf("unexpected record: %+v", m)
	}
	if err := st.ClearMailSent(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	list, _ = st.ListMailSent(ctx, 10)
	if len(list) != 0 {
		t.Fatalf("log must be empty, got %d", len(list))
	}
}

func TestEmailTokens(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	uid := createUser(t, st, "tokenuser")
	now := time.Now().UTC()

	mk := func(kind, hash string, ttl time.Duration) int64 {
		id, err := st.CreateEmailToken(ctx, EmailToken{
			UserID: uid, Email: "u@example.com", Kind: kind, TokenHash: hash,
			TS: now.UnixMilli(), ExpiresAt: now.Add(ttl).UnixMilli(),
		})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
		return id
	}

	good := mk(TokenResetPassword, "hash-good", time.Hour)
	if _, err := st.GetEmailToken(ctx, "hash-good", TokenResetPassword); err != nil {
		t.Fatalf("get good: %v", err)
	}
	// Действующий токен не находится под другим видом.
	if _, err := st.GetEmailToken(ctx, "hash-good", TokenVerifyEmail); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for wrong kind, got %v", err)
	}

	// Использованный токен больше не действует.
	if err := st.UseEmailToken(ctx, good); err != nil {
		t.Fatalf("use: %v", err)
	}
	if _, err := st.GetEmailToken(ctx, "hash-good", TokenResetPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after use, got %v", err)
	}

	// Просроченный токен не действует.
	mk(TokenResetPassword, "hash-expired", -time.Minute)
	if _, err := st.GetEmailToken(ctx, "hash-expired", TokenResetPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for expired, got %v", err)
	}

	// Повторная установка пароля отменяет прежние ссылки.
	mk(TokenResetPassword, "hash-new", time.Hour)
	if err := st.DeleteEmailTokens(ctx, uid, TokenResetPassword); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetEmailToken(ctx, "hash-new", TokenResetPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	// Очистка: удаляет просроченные и использованные.
	mk(TokenVerifyEmail, "hash-verify", time.Hour)
	if err := st.PurgeEmailTokens(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := st.GetEmailToken(ctx, "hash-expired", TokenResetPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token must be purged, got %v", err)
	}
	if _, err := st.GetEmailToken(ctx, "hash-verify", TokenVerifyEmail); err != nil {
		t.Fatalf("valid token must survive purge: %v", err)
	}
}

func TestGetUserByEmail(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	uid := createUser(t, st, "mailuser")
	db := st.(*DBStore)
	if _, err := db.db.ExecContext(ctx, db.bind("UPDATE users SET email = %s WHERE id = %s", 2),
		"Reader@Example.com", uid); err != nil {
		t.Fatalf("set email: %v", err)
	}
	u, err := st.GetUserByEmail(ctx, "reader@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if u.ID != uid {
		t.Fatalf("wrong user: %+v", u)
	}
	if _, err := st.GetUserByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := st.GetUserByEmail(ctx, "  "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty, got %v", err)
	}
}
