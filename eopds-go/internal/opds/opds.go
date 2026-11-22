// Package opds сериализует каталог библиотеки в OPDS 1.2 (Atom XML) и
// OPDS 2.0 (JSON). Один набор данных (Feed) — две тонкие сериализации,
// без дублирования логики выборки (её выполняет слой store).
package opds

// Link — ссылка записи/каталога.
type Link struct {
	Rel  string
	Href string
	Type string
	// Title — необязательный человекочитаемый заголовок ссылки.
	Title string
}

// Author — автор записи.
type Author struct {
	Name string
	URI  string
}

// Entry — запись каталога: книга (acquisition) или навигационная ссылка.
type Entry struct {
	ID      string
	Title   string
	Updated string // RFC3339
	Summary string
	Authors []Author
	Links   []Link
	// Content — HTML-описание (для JSON content).
	Content string
	// Categories — жанры (term/label).
	Categories []Category
}

// Category — категория записи (жанр).
type Category struct {
	Term  string
	Label string
}

// Feed — каталог OPDS (навигация или acquisita).
type Feed struct {
	ID      string
	Title   string
	Updated string // RFC3339
	// Kind: "navigation" | "acquisition".
	Kind    string
	Entries []Entry
	// Self — ссылка на сам feed (rel=self).
	Self Link
	// Next/Prev — ссылки пагинации (rel=next/prev), пустые если нет.
	Next string
	Prev string
	// Start — ссылка на корень каталога (rel=start).
	Start string
	// Search — OpenSearch описание (rel=search), пустая если нет.
	Search string
}

// Навигационные/типовые rel-значения.
const (
	RelSelf        = "self"
	RelStart       = "start"
	RelNext        = "next"
	RelPrevious    = "previous"
	RelSearch      = "search"
	RelSubsection  = "subsection"
	RelAcquisition = "http://opds-spec.org/acquisition"
	RelImage       = "http://opds-spec.org/image"
	RelThumbnail   = "http://opds-spec.org/image/thumbnail"
)

// Типы контента.
const (
	// TypeNavigation — Atom navigation feed (OPDS 1.2).
	TypeNavigation = "application/atom+xml;profile=opds-catalog;kind=navigation"
	// TypeAcquisition — Atom acquisition feed (OPDS 1.2).
	TypeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	// TypeOPDS2 — OPDS 2.0 JSON.
	TypeOPDS2 = "application/opds+json"
	// TypeSearch — OpenSearch description.
	TypeSearch = "application/opensearchdescription+xml"
)
