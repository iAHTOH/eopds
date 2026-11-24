package admin

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// csrfTestFuncs — заглушки CSRF-функций для разбора шаблонов в тестах:
// на этапе разбора html/template требует, чтобы функция существовала.
var csrfTestFuncs = template.FuncMap{
	"csrfField": func() template.HTML { return "" },
	"csrfToken": func() string { return "" },
}

// addCSRF добавляет заглушки CSRF в набор функций шаблона.
func addCSRF(fm template.FuncMap) template.FuncMap {
	for k, v := range csrfTestFuncs {
		fm[k] = v
	}
	return fm
}

// TestLibBookListRendersSeriesSeq проверяет, что в мини-карточке списка книг
// серия выводится вместе с номером книги в серии (например, «Конан #119»),
// как и на странице книги.
func TestLibBookListRendersSeriesSeq(t *testing.T) {
	tmpl := template.New("lib_book_list").Funcs(addCSRF(template.FuncMap{
		"T":    func(k string) string { return k },
		"kb":   func(n int64) string { return "0" },
		"trim": func(s string, n int) string { return s },
	}))
	if _, err := tmpl.ParseFS(templatesFS, "templates/lib_parts.html", "templates/library_books.html"); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := struct {
		Page struct {
			Items []store.LibraryBook
		}
		Pagination Pagination
		ShelfMode  bool
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

// TestBookCardRendersRatingAndComments проверяет, что карточка книги рендерит
// блок рейтинга (5 звёзд, половина) и отзывы для авторизованного пользователя.
func TestBookCardRendersRatingAndComments(t *testing.T) {
	tmpl := template.New("layout").Funcs(addCSRF(template.FuncMap{
		"T":        func(k string) string { return k },
		"kb":       func(n int64) string { return "0" },
		"trim":     func(s string, n int) string { return s },
		"initial":  func(s string) string { return s },
		"div2":     div2,
		"seq":      seq,
		"mul2":     mul2,
		"langName": langName,
		"langCode": langCode,
		"roleName": roleName,
	}))
	if _, err := tmpl.ParseFS(templatesFS,
		"templates/layout.html", "templates/chrome.html",
		"templates/lib_parts.html", "templates/library_entities.html",
		"templates/settings_parts.html", "templates/library_book.html"); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := map[string]any{
		"Book":          store.LibraryBook{ID: 42, Title: "Тест", Format: "fb2", SizeBytes: 10},
		"User":          &store.User{ID: 1, Username: "reader"},
		"Rating":        store.RatingSummary{Count: 3, Avg: 3.5, MyScore: 7},
		"StarsMax":      5,
		"CommentMaxLen": 4000,
		"Comments": []store.BookComment{
			{ID: 9, BookID: 42, UserID: 1, UserName: "reader", Body: "Отлично!", CreatedAt: "2026-01-01T00:00:00Z", CanDelete: true},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()
	// library.css обязан подключаться в layout (иначе стили рейтинга/отзывов
	// и виджет звёзд не применяются).
	if !strings.Contains(html, "/static/css/library.css") {
		t.Fatalf("layout не подключает library.css:\n%s", html)
	}
	for _, want := range []string{
		`class="book-rating"`,
		`class="card book-comments"`,
		`class="rating-widget"`,
		`type="hidden"`,
		"reviews.comments",
		"Отлично!",
		`action="/library/book/42/rate"`,
		`action="/library/book/42/comment"`,
		`class="comment-input"`,
		`class="dl-link comment-send"`,
		`class="star half"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("в карточке книги нет %q:\n%s", want, html)
		}
	}
	// Ползунок и отдельная кнопка «Оценить» больше не нужны: оценка — кликом.
	for _, bad := range []string{`type="range"`, `rating-range`} {
		if strings.Contains(html, bad) {
			t.Fatalf("в карточке осталось устаревшее: %q", bad)
		}
	}
}
