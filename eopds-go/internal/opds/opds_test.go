package opds

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sampleFeed(kind string) Feed {
	return Feed{
		ID:      "urn:test",
		Title:   "Test & <Feed>",
		Updated: "2026-01-01T00:00:00Z",
		Kind:    kind,
		Start:   "/opds",
		Self:    Link{Rel: RelSelf, Href: "/opds", Type: TypeNavigation},
		Entries: []Entry{
			{
				ID:      "urn:book:1",
				Title:   "Книга «Тест»",
				Updated: "2026-01-01T00:00:00Z",
				Summary: "Описание <b>жирное</b>",
				Authors: []Author{{Name: "Иван Петров"}},
				Links: []Link{
					{Rel: RelAcquisition, Href: "/library/book/1/download?fmt=fb2", Type: "application/x-fictionbook+xml", Title: "FB2"},
				},
				Categories: []Category{{Term: "sf", Label: "sf"}},
			},
		},
	}
}

func TestAtomWellFormedAndEscaped(t *testing.T) {
	var buf bytes.Buffer
	if err := Atom(&buf, sampleFeed("acquisition")); err != nil {
		t.Fatalf("atom: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "<?xml") {
		t.Fatalf("ожидался XML-заголовок")
	}
	if !strings.Contains(out, "<feed") || !strings.Contains(out, "</feed>") {
		t.Fatalf("нет <feed>: %s", out)
	}
	// Экранирование специальных символов.
	if strings.Contains(out, "<Feed>") {
		t.Fatalf("заголовок не экранирован: %s", out)
	}
	if !strings.Contains(out, "&lt;Feed&gt;") {
		t.Fatalf("ожидалось экранирование &lt;Feed&gt;: %s", out)
	}
	if !strings.Contains(out, `rel="http://opds-spec.org/acquisition"`) {
		t.Fatalf("нет acquisition-ссылки")
	}
	if !strings.Contains(out, "<entry>") {
		t.Fatalf("нет записи")
	}
}

func TestJSONValid(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleFeed("acquisition")); err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}
	pubs, ok := m["publications"].([]any)
	if !ok || len(pubs) != 1 {
		t.Fatalf("ожидалась одна публикация: %v", m["publications"])
	}
}

func TestJSONNavigation(t *testing.T) {
	f := Feed{
		ID: "urn:nav", Title: "Nav", Updated: "2026-01-01T00:00:00Z", Kind: "navigation",
		Entries: []Entry{
			{ID: "urn:nav:1", Title: "Авторы", Links: []Link{{Rel: RelSubsection, Href: "/opds/authors", Type: TypeNavigation}}},
		},
	}
	var buf bytes.Buffer
	if err := JSON(&buf, f); err != nil {
		t.Fatalf("json nav: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}
	nav, ok := m["navigation"].([]any)
	if !ok || len(nav) != 1 {
		t.Fatalf("ожидался 1 navigation link: %v", m["navigation"])
	}
	if _, has := m["publications"]; has {
		t.Fatalf("навигационный feed не должен содержать publications")
	}
}
