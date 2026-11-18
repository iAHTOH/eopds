package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Размеры/пороги как в старом веб-интерфейсе (EOPDS_MAXITEMS / EOPDS_SPLITITEMS).
const (
	libPageSize  = 60  // книг/сущностей на страницу
	libSplitMin  = 300 // порог для перехода на под-уровень
	libMaxDepth  = 3   // максимум уровней детализации префикса
	libPageLinks = 3   // половина окна ссылок пагинации
)

// langNames — подписи языковых фильтров (как в старом меню).
var langNames = map[int]string{
	1: "lang.cyrillic",
	2: "lang.latin",
	3: "lang.digits",
	9: "lang.other",
}

// libData — общие данные страниц библиотеки.
func (s *Server) libData(r *http.Request, section string) map[string]any {
	data := s.baseData(r)
	data["Nav"] = "library"
	data["LibSection"] = section
	data["LangMenu"] = langMenu
	return data
}

// langMenu — пункты языкового подменю (как в старом меню).
var langMenu = []LangTab{
	{Code: 1, Label: "lang.cyrillic"},
	{Code: 2, Label: "lang.latin"},
	{Code: 3, Label: "lang.digits"},
	{Code: 9, Label: "lang.other"},
	{Code: 0, Label: "lang.all"},
}

// parseLang читает ?lang= (0 = все).
// parseLang читает языковой фильтр ?lang= (1,2,3,9) либо cookie "lang_num".
// В отличие от языковой локали интерфейса (resolveLang), это фильтр БД.
func parseLang(r *http.Request) int {
	raw := r.URL.Query().Get("lang")
	if raw == "" {
		if c, err := r.Cookie("lang_num"); err == nil {
			raw = c.Value
		}
	}
	v, _ := strconv.Atoi(raw)
	switch v {
	case 1, 2, 3, 9:
		return v
	default:
		return 0
	}
}

// pageParam читает ?page= (>=1).
func pageParam(r *http.Request) int {
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		return v
	}
	return 1
}

// atoi64 парсит int64 (0 при ошибке).
func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// PrefixRow — строка указателя (префикс + количество + ссылка/флаг списка).
type PrefixRow struct {
	Key    string
	Count  int64
	IsList bool   // true — сразу список книг; false — переход на под-уровень
	URL    string // ссылка
}

// LangTab — вкладка языкового фильтра.
type LangTab struct {
	Code   int
	Label  string
	Active bool
}

// buildLangTabs собирает языковые вкладки для секции.
func (s *Server) buildLangTabs(r *http.Request, section string, cur int) []LangTab {
	order := []int{0, 1, 2, 3, 9}
	tabs := make([]LangTab, 0, len(order))
	for _, code := range order {
		label := "lang.all"
		if code != 0 {
			label = langNames[code]
		}
		tabs = append(tabs, LangTab{Code: code, Label: label, Active: code == cur})
	}
	return tabs
}

// libSectionBase — базовый путь секции (/library/authors и т.п.).
func (s *Server) libSectionBase(section string) string {
	switch section {
	case "authors":
		return "/library/authors"
	case "series":
		return "/library/series"
	case "genres":
		return "/library/genres"
	default:
		return "/library/books"
	}
}

// buildPrefixRows формирует строки указателя (префиксы length) для секции.
// kind: books|authors|series. prefix — уже набранный префикс. lang — фильтр.
func (s *Server) buildPrefixRows(ctx context.Context, st store.Store, section string,
	length int, prefix string, lang int) []PrefixRow {
	var items []store.AlphabetItem
	var err error
	switch section {
	case "authors":
		items, err = st.PrefixIndex(ctx, "authors", "search_full_name", length, prefix, 0)
	case "series":
		items, err = st.PrefixIndex(ctx, "series", "search_title", length, prefix, 0)
	default:
		items, err = st.BookByPrefixIndex(ctx, length, prefix, lang)
	}
	if err != nil {
		return nil
	}
	base := s.libSectionBase(section)
	rows := make([]PrefixRow, 0, len(items))
	for _, it := range items {
		row := PrefixRow{Key: it.Key, Count: it.Count}
		q := url.Values{}
		q.Set("lang", strconv.Itoa(lang))
		if it.Count < libSplitMin && len([]rune(it.Key)) >= length {
			// Мало элементов — сразу список.
			row.IsList = true
			q.Set("prefix", it.Key)
			q.Set("page", "1")
		} else {
			// Много — следующий уровень (chars).
			q.Set("chars", it.Key)
		}
		row.URL = base + "?" + q.Encode()
		rows = append(rows, row)
	}
	return rows
}

// handleLibraryIndex — /library → книги, все языки.
func (s *Server) handleLibraryIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/library/books?lang=0", http.StatusSeeOther)
}

// handleLibraryBooks — книги: указатель префиксов / список по prefix.
func (s *Server) handleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	s.handleLibraryEntity(w, r, "books")
}

// handleLibraryAuthors — авторы.
func (s *Server) handleLibraryAuthors(w http.ResponseWriter, r *http.Request) {
	s.handleLibraryEntity(w, r, "authors")
}

// handleLibrarySeries — серии.
func (s *Server) handleLibrarySeries(w http.ResponseWriter, r *http.Request) {
	s.handleLibraryEntity(w, r, "series")
}

// handleLibraryEntity — общий обработчик секций books/authors/series.
func (s *Server) handleLibraryEntity(w http.ResponseWriter, r *http.Request, section string) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	lang := parseLang(r)
	chars := r.URL.Query().Get("chars")
	prefix := r.URL.Query().Get("prefix")
	data := s.libData(r, section)
	// data["Lang"] оставляем локалью интерфейса (ru/en) из baseData;
	// числовой языковой фильтр храним отдельно, чтобы не ломать язык сайта.
	data["LangFilter"] = lang
	data["LangTabs"] = s.buildLangTabs(r, section, lang)

	// Список книг/сущностей по префиксу.
	// Фильтр книг по автору/серии/жанру (переход из списков сущностей).
	authorID := atoi64(r.URL.Query().Get("author"))
	seriesID := atoi64(r.URL.Query().Get("series"))
	genreID := atoi64(r.URL.Query().Get("genre"))
	if section == "books" && (authorID != 0 || seriesID != 0 || genreID != 0) {
		page := pageParam(r)
		pg, err := st.ListBooks(r.Context(), store.BooksFilter{AuthorID: authorID, SeriesID: seriesID, GenreID: genreID}, page, libPageSize)
		if err != nil {
			s.log.Error("library: filter books", "err", err)
		}
		data["ShowList"] = true
		data["Page"] = pg
		data["Breadcrumbs"] = []Crumb{{Label: "lib.tab_books", URL: "/library/books"}}
		data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
		s.render(w, r, templateFor(section), data)
		return
	}

	if prefix != "" {
		page := pageParam(r)
		data["ShowList"] = true
		data["Prefix"] = prefix
		data["Breadcrumbs"] = s.buildCrumbs(section, lang, prefix)
		if section == "books" {
			pg, err := st.ListBooksByPrefix(r.Context(), prefix, lang, page, libPageSize)
			if err != nil {
				s.log.Error("library: books by prefix", "err", err)
			}
			data["Page"] = pg
			data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
		} else {
			var items []store.NamedItem
			var total int64
			var err error
			if section == "authors" {
				items, total, err = st.ListAuthorsByPrefix(r.Context(), prefix, page, libPageSize)
			} else {
				items, total, err = st.ListSeriesByPrefix(r.Context(), prefix, page, libPageSize)
			}
			if err != nil {
				s.log.Error("library: entities by prefix", "err", err)
			}
			pages := int((total + libPageSize - 1) / libPageSize)
			data["Items"] = items
			data["Total"] = total
			data["Pagination"] = buildPagination(r.URL.Path, r, page, pages)
		}
		s.render(w, r, templateFor(section), data)
		return
	}

	// Указатель: длина префикса = len(chars)+1.
	level := len([]rune(chars)) + 1
	if level > libMaxDepth {
		level = libMaxDepth
	}
	rows := s.buildPrefixRows(r.Context(), st, section, level, chars, lang)
	data["ShowList"] = false
	data["PrefixRows"] = rows
	data["Breadcrumbs"] = s.buildCrumbs(section, lang, chars)
	data["Title"] = "nav.library"
	s.render(w, r, templateFor(section), data)
}

// templateFor возвращает имя шаблона страницы по секции.
func templateFor(section string) string {
	switch section {
	case "authors":
		return "library_authors"
	case "series":
		return "library_series"
	default:
		return "library_books"
	}
}

// Crumb — хлебная крошка.
type Crumb struct {
	Label string
	URL   string // пусто — текущая (без ссылки)
}

// buildCrumbs строит breadcrumbs: Раздел / Выбор / <язык> / <префикс...>.
func (s *Server) buildCrumbs(section string, lang int, prefix string) []Crumb {
	base := s.libSectionBase(section)
	sectionLabel := map[string]string{"books": "lib.tab_books", "authors": "lib.tab_authors", "series": "lib.tab_series"}[section]
	crumbs := []Crumb{{Label: sectionLabel, URL: base + "?lang=" + strconv.Itoa(lang)}}
	langLabel := "lang.all"
	if lang != 0 {
		langLabel = langNames[lang]
	}
	crumbs = append(crumbs, Crumb{Label: langLabel, URL: base + "?lang=" + strconv.Itoa(lang)})
	rumes := []rune(prefix)
	for i := 0; i < len(rumes); i++ {
		sub := string(rumes[:i+1])
		last := i == len(rumes)-1
		q := url.Values{}
		q.Set("lang", strconv.Itoa(lang))
		if last {
			q.Set("prefix", sub)
		} else {
			q.Set("chars", sub)
		}
		crumbs = append(crumbs, Crumb{Label: sub, URL: base + "?" + q.Encode()})
	}
	return crumbs
}

// handleLibraryGenres — список жанров (плоский, постранично).
func (s *Server) handleLibraryGenres(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	page := pageParam(r)
	data := s.libData(r, "genres")
	items, total, err := st.ListGenres(r.Context(), page, libPageSize)
	if err != nil {
		s.log.Error("library: genres", "err", err)
	}
	pages := int((total + libPageSize - 1) / libPageSize)
	data["Items"] = items
	data["Total"] = total
	data["Pagination"] = buildPagination(r.URL.Path, r, page, pages)
	s.render(w, r, "library_genres", data)
}

// handleLibraryBook — карточка книги.
func (s *Server) handleLibraryBook(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	id := atoi64(r.PathValue("id"))
	book, err := st.GetBook(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := s.libData(r, "books")
	data["Book"] = book
	s.render(w, r, "library_book", data)
}

// handleLibrarySearch — быстрый поиск по названию (?q=).
func (s *Server) handleLibrarySearch(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := s.libData(r, "books")
	data["Query"] = q
	data["ShowList"] = true
	if q != "" {
		page := pageParam(r)
		pg, err := st.SearchBooks(r.Context(), q, page, libPageSize)
		if err != nil {
			s.log.Error("library: search", "err", err)
		}
		data["Page"] = pg
		data["Breadcrumbs"] = []Crumb{{Label: "lib.tab_books", URL: "/library/books"}, {Label: "lib.search_results"}}
		data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
	}
	s.render(w, r, "library_books", data)
}

// PageLink — ссылка пагинации.
type PageLink struct {
	Num int
	URL string
	Cur bool
	Dot bool
}

// Pagination — данные пагинации.
type Pagination struct {
	Page    int
	Pages   int
	Prev    string
	Next    string
	HasPrev bool
	HasNext bool
	Links   []PageLink
}

// buildPagination формирует окно ссылок, сохраняя прочие query-параметры.
func buildPagination(path string, r *http.Request, page, pages int) Pagination {
	p := Pagination{Page: page, Pages: pages, HasPrev: page > 1, HasNext: page < pages}
	mkURL := func(n int) string {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(n))
		return path + "?" + q.Encode()
	}
	if p.HasPrev {
		p.Prev = mkURL(page - 1)
	}
	if p.HasNext {
		p.Next = mkURL(page + 1)
	}
	include := func(n int) bool {
		return n == 1 || n == pages || (n >= page-libPageLinks && n <= page+libPageLinks)
	}
	last := 0
	for n := 1; n <= pages; n++ {
		if !include(n) {
			continue
		}
		if last != 0 && n-last > 1 {
			p.Links = append(p.Links, PageLink{Dot: true})
		}
		p.Links = append(p.Links, PageLink{Num: n, URL: mkURL(n), Cur: n == page})
		last = n
	}
	return p
}

// handleBookCover отдаёт обложку книги (ленивое извлечение из файла + кэш).
// Если обложки нет — 404 (шаблон подставит заглушку через onerror).
func (s *Server) handleBookCover(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	id := atoi64(r.PathValue("id"))
	cover, err := st.BookCover(r.Context(), id)
	if err != nil || len(cover.Data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", cover.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(cover.Data)
}
