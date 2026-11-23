package store

import (
	"context"
	"testing"
	"time"
)

func TestUserProfileAndSessions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.CreateRootAdmin(ctx, "root", "pbkdf2-sha256$1$x$y", "root@example.com")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	// По умолчанию allow_adult=true, display_name пусто, аватар нет.
	u, err := st.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !u.AllowAdult {
		t.Errorf("allow_adult должен быть true по умолчанию")
	}
	if u.NameForGreeting() != "root" {
		t.Errorf("NameForGreeting без display_name = %q, want root", u.NameForGreeting())
	}

	// Обновление имени, email и предпочтений.
	em := "new@example.com"
	err = st.UpdateProfile(ctx, ProfileUpdate{
		UserID: id, DisplayName: "  Иван  ", Email: &em,
		PrefLang: "ru", PrefTheme: "light", ReadFont: "serif", ReadFontSize: 120,
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	u, _ = st.GetUserByID(ctx, id)
	if u.DisplayName != "Иван" {
		t.Errorf("display_name = %q, want Иван (с trim)", u.DisplayName)
	}
	if u.NameForGreeting() != "Иван" {
		t.Errorf("NameForGreeting = %q, want Иван", u.NameForGreeting())
	}
	if u.Email == nil || *u.Email != em {
		t.Errorf("email не обновился")
	}
	if u.PrefTheme != "light" || u.ReadFontSize != 120 || u.ReadFont != "serif" {
		t.Errorf("предпочтения не сохранились: %+v", u)
	}

	// Email == nil — не менять.
	err = st.UpdateProfile(ctx, ProfileUpdate{UserID: id, DisplayName: "Пётр"})
	if err != nil {
		t.Fatalf("update nil email: %v", err)
	}
	u, _ = st.GetUserByID(ctx, id)
	if u.Email == nil || *u.Email != em {
		t.Errorf("email должен сохраниться при Email=nil")
	}

	// Аватар.
	if err := st.SetUserAvatar(ctx, id, "u1.jpg"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	u, _ = st.GetUserByID(ctx, id)
	if u.Avatar != "u1.jpg" {
		t.Errorf("avatar = %q", u.Avatar)
	}

	// Сессии: 3 штуки, удаляем все кроме одной.
	for i, sid := range []string{"a", "b", "c"} {
		if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
	}
	n, err := st.DeleteUserSessionsExcept(ctx, id, "a")
	if err != nil {
		t.Fatalf("delete sessions: %v", err)
	}
	if n != 2 {
		t.Errorf("удалено сессий = %d, want 2", n)
	}
	if _, err := st.GetSessionUser(ctx, "a"); err != nil {
		t.Errorf("сессия a должна остаться: %v", err)
	}
	if _, err := st.GetSessionUser(ctx, "b"); err == nil {
		t.Errorf("сессия b должна быть удалена")
	}
}
