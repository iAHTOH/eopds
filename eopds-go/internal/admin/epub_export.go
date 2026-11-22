package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/epub"
	"github.com/iAHTOH/eopds/eopds-go/internal/fb2render"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// bookToEPUB собирает EPUB (2.0/3.0) из книги: читает исходный FB2, рендерит
// тело через fb2render (с выносом картинок в файлы) и упаковывает в EPUB.
// Возвращает ZIP-байты.
func bookToEPUB(ctx context.Context, st store.Store, book store.LibraryBook, version string, kobo bool) ([]byte, error) {
	src, err := st.BookFile(ctx, book.ID)
	if err != nil {
		return nil, err
	}
	defer src.Reader.Close()

	// Картинки ссылаются на файлы images/<id>; fb2render вернёт их байты.
	res, err := fb2render.Render(src.Reader, fb2render.Options{
		ImageURL:  epub.ImageHref,
		WithNotes: true,
	})
	if err != nil {
		return nil, err
	}

	images := make([]epub.Image, 0, len(res.Images))
	for _, im := range res.Images {
		images = append(images, epub.Image{ID: im.ID, MediaType: im.MediaType, Data: im.Data})
	}
	toc := make([]epub.TOCItem, 0, len(res.TOC))
	for _, t := range res.TOC {
		toc = append(toc, epub.TOCItem{ID: t.ID, Title: t.Title, Level: t.Level})
	}

	b := epub.Book{
		Title:      bookTitle(book),
		Lang:       epubLang(book.Lang),
		Authors:    bookAuthorNames(book),
		Genres:     bookGenreCodes(book),
		Annotation: book.Annotation,
		Identifier: "urn:eopds:book:" + strconv.FormatInt(book.ID, 10),
		Modified:   bookModified(book.DocDate),
		BodyHTML:   res.HTML,
		TOC:        toc,
		Images:     images,
	}
	return epub.Build(b, epub.Options{Version: version, Kobo: kobo})
}

// bookTitle — название книги (или имя файла без расширения).
func bookTitle(b store.LibraryBook) string {
	if t := strings.TrimSpace(b.Title); t != "" {
		return t
	}
	return downloadBaseName(b)
}

// bookAuthorNames — имена авторов.
func bookAuthorNames(b store.LibraryBook) []string {
	names := make([]string, 0, len(b.Authors))
	for _, a := range b.Authors {
		if strings.TrimSpace(a.Name) != "" {
			names = append(names, a.Name)
		}
	}
	return names
}

// bookGenreCodes — коды жанров.
func bookGenreCodes(b store.LibraryBook) []string {
	codes := make([]string, 0, len(b.Genres))
	for _, g := range b.Genres {
		if strings.TrimSpace(g.Code) != "" {
			codes = append(codes, g.Code)
		}
	}
	return codes
}

// epubLang нормализует код языка; пусто/неизвестно → "ru".
func epubLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "" {
		return "ru"
	}
	return l
}

// bookModified берёт дату документа (DocDate, YYYY-MM-DD) или текущее время.
func bookModified(docDate string) time.Time {
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(docDate)); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}
