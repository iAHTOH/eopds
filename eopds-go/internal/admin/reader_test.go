package admin

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

func TestParsePercent(t *testing.T) {
	cases := map[string]float64{
		"":              0,
		"percent:":      0,
		"percent:0":     0,
		"percent:12.5":  12.5,
		"percent:100":   100,
		"percent:101":   0, // вне диапазона
		"percent:-5":    0,
		"percent:abc":   0,
		"position:12.5": 0, // не наш формат
	}
	for in, want := range cases {
		if got := parsePercent(in); got != want {
			t.Errorf("parsePercent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReadLimit(t *testing.T) {
	s, err := readLimit(strings.NewReader("hello"), 10)
	if err != nil || s != "hello" {
		t.Fatalf("readLimit ok-case: %q, %v", s, err)
	}
	if _, err := readLimit(strings.NewReader("0123456789"), 5); err != errTooLarge {
		t.Fatalf("readLimit too-large: ожидалась errTooLarge, получено %v", err)
	}
}

// TestReaderTemplateParses проверяет, что шаблон читалки корректно собирается
// вместе с общими частями и содержит ожидаемые элементы.
func TestReaderTemplateParses(t *testing.T) {
	tmpl := template.New("reader").Funcs(addCSRF(template.FuncMap{
		"T":        func(k string) string { return k },
		"langName": langName,
		"langCode": langCode,
		"roleName": roleName,
		"kb":       func(n int64) string { return "0" },
		"trim":     func(s string, n int) string { return s },
		"initial":  greetingInitial,
	}))
	if _, err := tmpl.ParseFS(templatesFS,
		"templates/layout.html", "templates/chrome.html", "templates/reader.html"); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := map[string]any{
		"Book":         store.LibraryBook{ID: 42, Title: "Книга", Format: "fb2"},
		"StartPercent": 12.5,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`id="reader-body"`,
		`data-book-id="42"`,
		`/html`,
		`/progress`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("в шаблоне читалки нет %q:\n%s", want, html)
		}
	}
}
