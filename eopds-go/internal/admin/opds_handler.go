package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/opds"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// opdsPageSize — книг/section на один OPDS-feed.
const opdsPageSize = 50

// opdsKind определяет формат ответа: 1.2 (Atom) или 2.0 (JSON).
// Правила: явный ?format=json|atom; заголовок Accept с application/opds+json → 2.0;
// иначе 1.2 (Atom, по умолчанию — его понимают все читалки).
func opdsWantJSON(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "json", "opds2", "2":
		return true
	case "atom", "xml", "opds1", "1":
		return false
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/opds+json") {
		// Если клиент явно просит только JSON — JSON; иначе Atom имеет приоритет
		// для совместимости. Здесь: JSON только если Atom не запрошен.
		if !strings.Contains(accept, "atom+xml") {
			return true
		}
	}
	return false
}

// setOPDSHeaders выставляет Content-Type и добавляет альтернативную ссылку
// на другой формат (через Link-заголовок HTTP), чтобы клиенты могли найти оба.
func opdsContentType(f opds.Feed, wantJSON bool) string {
	if wantJSON {
		return opds.TypeOPDS2
	}
	if f.Kind == "navigation" {
		return opds.TypeNavigation
	}
	return opds.TypeAcquisition
}

// writeOPDS сериализует feed в выбранном формате.
func (s *Server) writeOPDS(w http.ResponseWriter, r *http.Request, f opds.Feed) {
	wantJSON := opdsWantJSON(r)
	w.Header().Set("Content-Type", opdsContentType(f, wantJSON)+"; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if wantJSON {
		if err := opds.JSON(w, f); err != nil {
			s.log.Error("opds: json encode", "err", err)
		}
		return
	}
	if err := opds.Atom(w, f); err != nil {
		s.log.Error("opds: atom encode", "err", err)
	}
}

func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }

// bookEntry формирует OPDS-запись книги с acquisition-ссылками.
func bookEntry(b store.LibraryBook) opds.Entry {
	id := "urn:eopds:book:" + strconv.FormatInt(b.ID, 10)
	e := opds.Entry{
		ID:      id,
		Title:   b.Title,
		Updated: nowRFC(),
		Summary: strings.TrimSpace(b.Annotation),
	}
	for _, a := range b.Authors {
		e.Authors = append(e.Authors, opds.Author{Name: a.Name})
	}
	if len(e.Authors) == 0 {
		e.Authors = append(e.Authors, opds.Author{Name: "Неизвестный автор"})
	}
	// Обложка.
	e.Links = append(e.Links,
		opds.Link{Rel: opds.RelImage, Href: "/library/book/" + strconv.FormatInt(b.ID, 10) + "/cover", Type: "image/jpeg"},
		opds.Link{Rel: opds.RelThumbnail, Href: "/library/book/" + strconv.FormatInt(b.ID, 10) + "/cover", Type: "image/jpeg"},
	)
	base := "/library/book/" + strconv.FormatInt(b.ID, 10) + "/download"
	// Исходный файл.
	e.Links = append(e.Links, opds.Link{
		Rel:   opds.RelAcquisition,
		Href:  base + "?fmt=" + b.Format,
		Type:  mimeForFormat(b.Format),
		Title: strings.ToUpper(b.Format),
	})
	// ZIP для fb2.
	if strings.EqualFold(b.Format, "fb2") {
		e.Links = append(e.Links, opds.Link{
			Rel:   opds.RelAcquisition,
			Href:  base + "?fmt=fb2%2Bzip",
			Type:  "application/zip",
			Title: "FB2 (zip)",
		})
	}
	for _, c := range b.Genres {
		e.Categories = append(e.Categories, opds.Category{Term: c.Code, Label: c.Code})
	}
	return e
}

// mimeForFormat — MIME-тип для формата книги.
func mimeForFormat(format string) string {
	switch strings.ToLower(format) {
	case "fb2":
		return "application/x-fictionbook+xml"
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "txt":
		return "text/plain"
	case "md", "markdown":
		return "text/markdown"
	default:
		return "application/octet-stream"
	}
}

// navEntry — навигационная запись (ссылка на подкаталог).
func navEntry(title, href, summary string) opds.Entry {
	return opds.Entry{
		ID:      "urn:eopds:nav:" + href,
		Title:   title,
		Updated: nowRFC(),
		Summary: summary,
		Links:   []opds.Link{{Rel: opds.RelSubsection, Href: href, Type: opds.TypeNavigation, Title: title}},
	}
}

// handleOPDSRoot — корневой navigation feed.
func (s *Server) handleOPDSRoot(w http.ResponseWriter, r *http.Request) {
	feed := opds.Feed{
		ID:      "urn:eopds:root",
		Title:   "Easy OPDS — Каталог",
		Updated: nowRFC(),
		Kind:    "navigation",
		Start:   "/opds",
		Search:  "/opds/search.xml",
		Self:    opds.Link{Rel: opds.RelSelf, Href: "/opds", Type: opds.TypeNavigation},
	}
	feed.Entries = []opds.Entry{
		navEntry("Новые поступления", "/opds/new", "Последние добавленные книги"),
		navEntry("Все книги", "/opds/books", "Каталог всех книг"),
		navEntry("Авторы", "/opds/authors", "Книги по авторам"),
		navEntry("Серии", "/opds/series", "Книги по сериям"),
		navEntry("Жанры", "/opds/genres", "Книги по жанрам"),
	}
	s.writeOPDS(w, r, feed)
}

// handleOPDSNew — новые поступления (id DESC).
func (s *Server) handleOPDSNew(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	page := pageParam(r)
	pg, err := st.LatestBooks(r.Context(), page, opdsPageSize)
	if err != nil {
		s.log.Error("opds: new", "err", err)
	}
	feed := opds.Feed{
		ID:      "urn:eopds:new",
		Title:   "Новые поступления",
		Updated: nowRFC(),
		Kind:    "acquisition",
		Start:   "/opds",
		Self:    opds.Link{Rel: opds.RelSelf, Href: "/opds/new", Type: opds.TypeAcquisition},
	}
	feed.Entries = booksToEntries(pg.Items)
	feed.Next = pgNext(r, pg)
	feed.Prev = pgPrev(r, pg)
	s.writeOPDS(w, r, feed)
}

// handleOPDSBooks — все книги (по алфавиту), с фильтрами author/series/genre.
func (s *Server) handleOPDSBooks(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	page := pageParam(r)
	f := store.BooksFilter{
		AuthorID: atoi64(r.URL.Query().Get("author")),
		SeriesID: atoi64(r.URL.Query().Get("series")),
		GenreID:  atoi64(r.URL.Query().Get("genre")),
	}
	pg, err := st.ListBooks(r.Context(), f, page, opdsPageSize)
	if err != nil {
		s.log.Error("opds: books", "err", err)
	}
	feed := opds.Feed{
		ID:      "urn:eopds:books",
		Title:   "Все книги",
		Updated: nowRFC(),
		Kind:    "acquisition",
		Start:   "/opds",
		Self:    opds.Link{Rel: opds.RelSelf, Href: "/opds/books", Type: opds.TypeAcquisition},
	}
	feed.Entries = booksToEntries(pg.Items)
	feed.Next = pgNext(r, pg)
	feed.Prev = pgPrev(r, pg)
	s.writeOPDS(w, r, feed)
}

// handleOPDSSearch — поиск по названию (?q=).
func (s *Server) handleOPDSSearch(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// opensearch формат: searchTerms.
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("searchTerms"))
	}
	page := pageParam(r)
	feed := opds.Feed{
		ID:      "urn:eopds:search",
		Title:   "Поиск: " + q,
		Updated: nowRFC(),
		Kind:    "acquisition",
		Start:   "/opds",
		Self:    opds.Link{Rel: opds.RelSelf, Href: "/opds/search?q=" + url.QueryEscape(q), Type: opds.TypeAcquisition},
	}
	if q != "" {
		pg, err := st.SearchBooks(r.Context(), q, page, opdsPageSize)
		if err != nil {
			s.log.Error("opds: search", "err", err)
		}
		feed.Entries = booksToEntries(pg.Items)
		feed.Next = pgNext(r, pg)
		feed.Prev = pgPrev(r, pg)
	}
	s.writeOPDS(w, r, feed)
}

// handleOPDSAuthors — navigation feed авторов (постранично).
func (s *Server) handleOPDSAuthors(w http.ResponseWriter, r *http.Request) {
	s.opdsEntities(w, r, "authors")
}

// handleOPDSSeries — navigation feed серий.
func (s *Server) handleOPDSSeries(w http.ResponseWriter, r *http.Request) {
	s.opdsEntities(w, r, "series")
}

// handleOPDSGenres — navigation feed жанров.
func (s *Server) handleOPDSGenres(w http.ResponseWriter, r *http.Request) {
	s.opdsEntities(w, r, "genres")
}

// opdsEntities — общий обработчик навигационных списков авторов/серий/жанров.
func (s *Server) opdsEntities(w http.ResponseWriter, r *http.Request, kind string) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	page := pageParam(r)
	prefix := r.URL.Query().Get("prefix")
	var (
		items []store.NamedItem
		total int64
		err   error
	)
	filterParam := ""
	switch kind {
	case "authors":
		items, total, err = st.ListAuthors(r.Context(), prefix, page, opdsPageSize)
		filterParam = "author"
	case "series":
		items, total, err = st.ListSeries(r.Context(), prefix, page, opdsPageSize)
		filterParam = "series"
	case "genres":
		items, total, err = st.ListGenres(r.Context(), page, opdsPageSize)
		filterParam = "genre"
	}
	if err != nil {
		s.log.Error("opds: entities", "kind", kind, "err", err)
	}

	title := map[string]string{"authors": "Авторы", "series": "Серии", "genres": "Жанры"}[kind]
	selfHref := "/opds/" + kind
	feed := opds.Feed{
		ID:      "urn:eopds:" + kind,
		Title:   title,
		Updated: nowRFC(),
		Kind:    "navigation",
		Start:   "/opds",
		Self:    opds.Link{Rel: opds.RelSelf, Href: selfHref, Type: opds.TypeNavigation},
	}
	for _, it := range items {
		href := fmt.Sprintf("/opds/books?%s=%d", filterParam, it.ID)
		feed.Entries = append(feed.Entries, navEntry(
			fmt.Sprintf("%s (%d)", it.Title, it.Count), href, ""))
	}
	pages := int((total + int64(opdsPageSize) - 1) / int64(opdsPageSize))
	if page < pages {
		feed.Next = selfHref + "?page=" + strconv.Itoa(page+1)
	}
	if page > 1 {
		feed.Prev = selfHref + "?page=" + strconv.Itoa(page-1)
	}
	s.writeOPDS(w, r, feed)
}

// handleOPDSSearchDescription — OpenSearch описание (rel=search).
func (s *Server) handleOPDSSearchDescription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", opds.TypeSearch+"; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>Easy OPDS</ShortName>
  <Description>Поиск книг по названию</Description>
  <InputEncoding>UTF-8</InputEncoding>
  <OutputEncoding>UTF-8</OutputEncoding>
  <Url type="application/atom+xml" template="/opds/search?q={searchTerms}"/>
</OpenSearchDescription>`)
}

// booksToEntries конвертирует книги в OPDS-записи.
func booksToEntries(items []store.LibraryBook) []opds.Entry {
	out := make([]opds.Entry, 0, len(items))
	for _, b := range items {
		out = append(out, bookEntry(b))
	}
	return out
}

// pgNext/pgPrev — ссылки пагинации feed (сохраняя имеющиеся query-параметры).
func pgNext(r *http.Request, pg store.LibraryPage) string {
	if pg.Page < pg.Pages {
		return withPage(r, pg.Page+1)
	}
	return ""
}

func pgPrev(r *http.Request, pg store.LibraryPage) string {
	if pg.Page > 1 {
		return withPage(r, pg.Page-1)
	}
	return ""
}

// withPage — URL текущего feed с номером страницы (сохраняя прочие параметры).
func withPage(r *http.Request, page int) string {
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(page))
	return r.URL.Path + "?" + q.Encode()
}
