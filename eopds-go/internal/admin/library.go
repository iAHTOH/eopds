package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
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
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	cats, err := st.ListRootCatalogs(r.Context())
	if err != nil {
		s.log.Error("library: root catalogs", "err", err)
	}
	data := s.libData(r, "")
	data["RootCatalogs"] = cats
	s.render(w, r, "library", data)
}

// handleLibrarySuggest — JSON-подсказки для живого поиска (autocomplete).
// GET /library/suggest?q=...&by=title|author|series → [{id,title,authors,annotation,format}]
func (s *Server) handleLibrarySuggest(w http.ResponseWriter, r *http.Request) {
	empty := struct {
		Items []suggestItem `json:"items"`
	}{Items: []suggestItem{}}
	st := s.getStore()
	if st == nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	by := r.URL.Query().Get("by")
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	books, err := st.SuggestBooks(r.Context(), q, by, 5)
	if err != nil {
		s.log.Error("library: suggest", "err", err)
		writeJSON(w, http.StatusInternalServerError, empty)
		return
	}
	out := struct {
		Items []suggestItem `json:"items"`
	}{Items: make([]suggestItem, 0, len(books))}
	for _, b := range books {
		names := make([]string, 0, len(b.Authors))
		for _, a := range b.Authors {
			names = append(names, a.Name)
		}
		out.Items = append(out.Items, suggestItem{
			ID:         b.ID,
			Title:      b.Title,
			Authors:    names,
			Annotation: trimText(b.Annotation, 240),
			Format:     b.Format,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// suggestItem — мини-карточка книги для автоподсказок.
type suggestItem struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Authors    []string `json:"authors,omitempty"`
	Annotation string   `json:"annotation,omitempty"`
	Format     string   `json:"format,omitempty"`
}

// handleLibraryCatalog показывает содержимое каталога/папки: подэлементы
// (вложенные папки, zip) и книги, лежащие непосредственно в этом узле.
func (s *Server) handleLibraryCatalog(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	id := atoi64(r.PathValue("id"))
	cat, err := st.GetCatalog(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	children, err := st.ListChildCatalogs(r.Context(), id)
	if err != nil {
		s.log.Error("library: child catalogs", "err", err)
	}
	page := pageParam(r)
	books, err := st.ListBooksInCatalog(r.Context(), id, page, libPageSize)
	if err != nil {
		s.log.Error("library: catalog books", "err", err)
	}
	data := s.libData(r, "")
	data["Catalog"] = cat
	data["Children"] = children
	data["ShowList"] = true
	data["Page"] = books
	data["Breadcrumbs"] = s.buildCatalogCrumbs(r, st, cat)
	data["Pagination"] = buildPagination(r.URL.Path, r, books.Page, books.Pages)
	s.render(w, r, "library_catalog", data)
}

// buildCatalogCrumbs строит хлебные крошки от корня до текущего каталога.
func (s *Server) buildCatalogCrumbs(r *http.Request, st store.Store, cat store.Catalog) []Crumb {
	var chain []store.Catalog
	cur := cat
	for {
		chain = append([]store.Catalog{cur}, chain...)
		if cur.ParentID == 0 {
			break
		}
		parent, err := st.GetCatalog(r.Context(), cur.ParentID)
		if err != nil {
			break
		}
		cur = parent
	}
	crumbs := []Crumb{{Label: "cat.catalogs", URL: "/library"}}
	for i, c := range chain {
		label := c.Name
		if label == "." {
			label = "lib.root"
		}
		if i == len(chain)-1 {
			crumbs = append(crumbs, Crumb{Label: label})
		} else {
			crumbs = append(crumbs, Crumb{Label: label, URL: "/library/catalog/" + itoa64(c.ID)})
		}
	}
	return crumbs
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

// handleBookDownload отдаёт файл книги авторизованному пользователю.
// GET /library/book/{id}/download?fmt=<fmt> — поток файла (с диска или из ZIP)
// как вложение. fmt: пусто/"<исходный>" — как есть; "fb2+zip" — ZIP;
// "epub"/"epub3"/"epub2"/"kepub" — конвертация FB2->EPUB (см. roadmap);
// прочие форматы (azw8/kfx/pdf/txt/md) пока 404 (конвертер не подключён).
func (s *Server) handleBookDownload(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	id := atoi64(r.PathValue("id"))
	book, err := st.GetBook(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fmtParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("fmt")))

	// Штатно: скачивание кладёт книгу на полку пользователя (для будущей
	// «книжной полки»). Ошибку не прерываем — она не критична для выдачи файла.
	if u := currentUser(r); u != nil {
		if err := st.AddToShelf(r.Context(), u.ID, id); err != nil {
			s.log.Error("library: add to shelf", "id", id, "err", err)
		}
	}

	src, err := st.BookFile(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer src.Reader.Close()

	base := downloadBaseName(book)
	switch fmtParam {
	case "", book.Format:
		// Отдать исходный файл как есть.
		name := base + "." + book.Format
		w.Header().Set("Content-Type", src.ContentType)
		if src.Size >= 0 {
			w.Header().Set("Content-Length", itoa64(src.Size))
		}
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
		if _, err := io.Copy(w, src.Reader); err != nil {
			s.log.Error("library: download", "id", id, "err", err)
		} else {
			s.recordDownload(r, book, id, book.Format)
		}
	case "fb2+zip", "zip":
		// Упаковать файл в ZIP на лету. Имя внутри архива — с расширением книги.
		data, err := io.ReadAll(src.Reader)
		if err != nil {
			s.log.Error("library: download read", "id", id, "err", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		entry, err := zw.Create(base + "." + book.Format)
		if err != nil {
			http.Error(w, "zip error", http.StatusInternalServerError)
			return
		}
		if _, err := entry.Write(data); err != nil {
			http.Error(w, "zip error", http.StatusInternalServerError)
			return
		}
		if err := zw.Close(); err != nil {
			http.Error(w, "zip error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", itoa64(int64(buf.Len())))
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(base+".zip"))
		_, _ = w.Write(buf.Bytes())
		s.recordDownload(r, book, id, "fb2+zip")
	case "epub", "epub3", "epub2", "kepub":
		// Конвертация в EPUB из FB2. Версия: epub2 -> 2.0, прочее -> 3.0.
		// (Kobo читает EPUB3; koboSpan-разметка — отдельная задача.)
		if !strings.EqualFold(book.Format, "fb2") {
			http.NotFound(w, r)
			return
		}
		ver := "3.0"
		ext := ".epub"
		kobo := false
		if fmtParam == "epub2" {
			ver = "2.0"
		}
		if fmtParam == "kepub" {
			ext = ".kepub.epub"
			kobo = true
		}
		data, err := bookToEPUB(r.Context(), st, book, ver, kobo)
		if err != nil {
			s.log.Error("library: epub build", "id", id, "err", err)
			http.Error(w, "epub error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("Content-Length", itoa64(int64(len(data))))
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(base+ext))
		_, _ = w.Write(data)
		s.recordDownload(r, book, id, fmtParam)
	default:
		// Конвертация в другие форматы — не подключена (см. roadmap).
		http.NotFound(w, r)
	}
}

// downloadBaseName формирует имя файла для скачивания из названия книги
// (без расширения), безопасное для заголовков.
func downloadBaseName(b store.LibraryBook) string {
	name := strings.TrimSpace(b.Title)
	if name == "" {
		// Без названия — имя исходного файла без расширения.
		name = strings.TrimSuffix(b.Filename, filepath.Ext(b.Filename))
	}
	if name == "" {
		name = "book"
	}
	return name
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
