package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/i18n"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

func TestEventViewsFormatting(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n load: %v", err)
	}
	tr := b.Translator("ru")

	evs := []store.SystemEvent{
		{TS: 1_700_000_000_000, Level: store.EventInfo, Category: "book.download",
			Actor: "Вася", ActorRole: "reader",
			Args: map[string]string{"book": "Маугли", "_book_id": "42", "_user_id": "7"}},
		{TS: 1_700_000_001_000, Level: store.EventWarn, Category: "auth.login_failed",
			Args: map[string]string{"user": "hacker"}},
		{TS: 1_700_000_002_000, Level: store.EventError, Category: "scan.error",
			Args: map[string]string{"path": "dir/a.zip/x.fb2", "error": "bad"}},
		{TS: 1_700_000_003_000, Level: store.EventInfo, Category: "book.rate",
			Actor: "reader", ActorRole: "reader",
			Args: map[string]string{"book": "Маугли", "stars": "3.5", "_book_id": "42", "_user_id": "4"}},
	}
	views := eventViews(evs, tr, time.UTC)
	if len(views) != 4 {
		t.Fatalf("len = %d", len(views))
	}
	if views[0].ActorName != "Вася" || views[0].ActorRole != "reader" {
		t.Fatalf("actor view = %+v", views[0])
	}
	if views[0].ActorText != "читатель" {
		t.Fatalf("actorText = %q, want читатель", views[0].ActorText)
	}
	if !strings.Contains(string(views[0].Message), "Маугли") || !strings.Contains(string(views[0].Message), "скачал") {
		t.Fatalf("download msg = %q", views[0].Message)
	}
	// Ссылки на профиль и карточку книги из служебных args.
	if views[0].ActorLink != "/settings/profile?u=7" {
		t.Fatalf("actorLink = %q", views[0].ActorLink)
	}
	if views[0].BookLink != "/library/book/42" {
		t.Fatalf("bookLink = %q", views[0].BookLink)
	}
	// Название книги внутри сообщения — ссылка на карточку.
	if !strings.Contains(string(views[0].Message), `href="/library/book/42"`) {
		t.Fatalf("book link not embedded: %q", views[0].Message)
	}
	// Оценка в звёздах подставляется.
	if !strings.Contains(string(views[3].Message), "3.5") {
		t.Fatalf("rate msg = %q", views[3].Message)
	}
	// login_failed: actor пуст, имя — из args.user.
	if views[1].ActorName != "" {
		t.Fatalf("login_failed actorName = %q, want ''", views[1].ActorName)
	}
	if !strings.Contains(string(views[1].Message), "hacker") {
		t.Fatalf("login_failed msg = %q", views[1].Message)
	}
	// scan.error: путь и причина.
	if !strings.Contains(string(views[2].Message), "dir/a.zip/x.fb2") || !strings.Contains(string(views[2].Message), "bad") {
		t.Fatalf("scan.error msg = %q", views[2].Message)
	}
	// Служебные ключи «_» не должны попадать в текст.
	if strings.Contains(string(views[0].Message), "_book_id") || strings.Contains(string(views[0].Message), "_user_id") {
		t.Fatalf("служебные ключи попали в текст: %q", views[0].Message)
	}
}

func TestCanViewEvents(t *testing.T) {
	cases := []struct {
		u    *store.User
		want bool
	}{
		{nil, false},
		{&store.User{Role: "reader"}, false},
		{&store.User{Role: "moderator"}, true},
		{&store.User{Role: "admin"}, true},
		{&store.User{Role: "reader", IsRoot: true}, true},
	}
	for _, c := range cases {
		if got := canViewEvents(c.u); got != c.want {
			t.Errorf("canViewEvents(%+v) = %v, want %v", c.u, got, c.want)
		}
	}
}
