package admin

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// TestLibBookListRendersSeriesSeq проверяет, что в мини-карточке списка книг
// серия выводится вместе с номером книги в серии (например, «Конан #119»),
// как и на странице книги.
func TestLibBookListRendersSeriesSeq(t *testing.T) {
	tmpl := template.New("lib_book_list").Funcs(template.FuncMap{
		"T":    func(k string) string { return k },
		"kb":   func(n int64) string { return "0" },
		"trim": func(s string, n int) string { return s },
	})
	if _, err := tmpl.ParseFS(templatesFS, "templates/lib_parts.html", "templates/library_books.html"); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := struct {
		Page struct {
			Items []store.LibraryBook
		}
		Pagination Pagination
	}{}
	data.Page.Items = []store.LibraryBook{{
		ID:        51967,
		Title:     "Вендия 1. Город у священной реки",
		SizeBytes: 354000,
		Series:    []store.SeriesRef{{ID: 8219, Title: "Конан", Seq: 119}},
	}}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "lib_book_list", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, "Конан") {
		t.Fatalf("в мини-карточке нет названия серии:\n%s", html)
	}
	if !strings.Contains(html, "#119") {
		t.Fatalf("в мини-карточке нет номера серии (#119):\n%s", html)
	}
}
