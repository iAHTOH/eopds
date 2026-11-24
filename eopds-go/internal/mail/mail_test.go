package mail

import (
	"context"
	"strings"
	"testing"
)

func TestSettingsValidate(t *testing.T) {
	base := Settings{Enabled: true, Host: "smtp.example.com", Port: 587,
		Security: SecuritySTARTTLS, FromAddr: "lib@example.com"}

	tests := []struct {
		name string
		mod  func(*Settings)
		ok   bool
	}{
		{"valid", func(s *Settings) {}, true},
		{"valid with auth", func(s *Settings) { s.Username = "u"; s.Password = "p" }, true},
		{"empty host", func(s *Settings) { s.Host = " " }, false},
		{"bad port", func(s *Settings) { s.Port = 0 }, false},
		{"bad security", func(s *Settings) { s.Security = "ssl3" }, false},
		{"empty from", func(s *Settings) { s.FromAddr = "" }, false},
		{"bad from", func(s *Settings) { s.FromAddr = "not-an-address" }, false},
		{"bad reply-to", func(s *Settings) { s.ReplyTo = "x@" }, false},
		{"password without tls", func(s *Settings) {
			s.Security = SecurityNone
			s.Username = "u"
			s.Password = "p"
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mod(&s)
			err := s.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestConfiguredAndHelpers(t *testing.T) {
	if (Settings{}).Configured() {
		t.Fatal("empty settings must not be configured")
	}
	if !(Settings{Host: "h", Port: 25, FromAddr: "a@b.c"}).Configured() {
		t.Fatal("settings must be configured")
	}
	if DefaultPort(SecuritySMTPS) != 465 || DefaultPort(SecuritySTARTTLS) != 587 ||
		DefaultPort(SecurityNone) != 25 {
		t.Fatal("unexpected default ports")
	}
	if NormalizeSecurity("SSL") != SecuritySMTPS || NormalizeSecurity("") != SecuritySTARTTLS ||
		NormalizeSecurity("plain") != SecurityNone {
		t.Fatal("normalize security")
	}
	if PortFromString("2525", 25) != 2525 || PortFromString("x", 587) != 587 ||
		PortFromString("70000", 25) != 25 {
		t.Fatal("port from string")
	}
}

func TestSendDisabled(t *testing.T) {
	c := NewClient()
	res := c.Send(context.Background(), Settings{}, Message{To: "a@b.c"})
	if res.Err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", res.Err)
	}
}

func TestVerifyBadSettings(t *testing.T) {
	c := NewClient()
	err := c.Verify(context.Background(), Settings{Host: "", Port: 0})
	if err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestLetters(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		l := PasswordReset(lang, "Библиотека", "Иван", "https://lib/reset/confirm?token=T", "1 час")
		if l.Subject == "" || l.Text == "" || l.HTML == "" {
			t.Fatalf("%s: empty letter parts", lang)
		}
		if !strings.Contains(l.Text, "https://lib/reset/confirm?token=T") {
			t.Fatalf("%s: link missing in text", lang)
		}
		if !strings.Contains(l.HTML, "token=T") {
			t.Fatalf("%s: link missing in html", lang)
		}
		tl := Test(lang, "Библиотека", "smtp.example.com:587")
		if tl.Subject == "" || !strings.Contains(tl.Text, "smtp.example.com:587") {
			t.Fatalf("%s: bad test letter", lang)
		}
	}
	// HTML-экранирование имени пользователя и ссылки.
	l := PasswordReset("ru", "Библ", "<script>", "https://x/?a=1&b=2", "1 час")
	if strings.Contains(l.HTML, "<script>") {
		t.Fatal("html must be escaped")
	}
	if !strings.Contains(l.HTML, "a=1&amp;b=2") {
		t.Fatal("link must be escaped in html")
	}
	// Неизвестный язык → en.
	if Lang("de") != "en" || Lang("ru-RU") != "ru" || Lang("") != "en" {
		t.Fatal("lang selection")
	}
}
