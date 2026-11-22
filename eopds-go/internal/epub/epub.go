// Package epub собирает EPUB (2.0/3.0) из отрендеренного содержимого книги.
//
// Вход — нейтральные данные (метаданные + HTML-тело + оглавление + картинки),
// без зависимости от store. Выход — ZIP-байты, пригодные для отдачи как
// application/epub+zip. Вёрстку тела выполняет fb2render.
package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"strings"
	"time"
)

// Image — картинка для включения в архив.
type Image struct {
	ID        string // исходный ссылочный id (href без '#')
	MediaType string
	Data      []byte
}

// TOCItem — элемент оглавления.
type TOCItem struct {
	ID    string
	Title string
	Level int
}

// Book — данные для сборки EPUB.
type Book struct {
	Title      string
	Lang       string // код языка (ru/en/...); пусто — "ru"
	Authors    []string
	Genres     []string
	Annotation string // HTML или текст (экранируется, если не HTML — упрощённо)
	Identifier string // уникальный id (например, urn:eopds:book:123)
	Modified   time.Time

	// BodyHTML — тело книги (фрагмент), картинки в котором ссылаются на
	// images/<id>.<ext> (тот же id, что в Image.ID).
	BodyHTML string
	TOC      []TOCItem
	Images   []Image
}

// Options — параметры генерации.
type Options struct {
	// Version: "3.0" (по умолчанию) или "2.0".
	Version string
}

const nsXHTML = "http://www.w3.org/1999/xhtml"

// Build собирает EPUB и возвращает байты ZIP.
func Build(b Book, opt Options) ([]byte, error) {
	version := opt.Version
	if version == "" {
		version = "3.0"
	}
	lang := b.Lang
	if lang == "" {
		lang = "ru"
	}
	modified := b.Modified
	if modified.IsZero() {
		modified = time.Now().UTC()
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype — первым, без сжатия, как требует спецификация EPUB.
	if err := writeStored(zw, "mimetype", []byte("application/epub+zip")); err != nil {
		return nil, err
	}

	files := map[string][]byte{
		"META-INF/container.xml": []byte(containerXML()),
	}
	// Изображения: images/<sanitize(id)> (без расширения; media-type в manifest).
	imgTypes := map[string]string{} // href -> media-type
	for i, im := range b.Images {
		href := ImageHref(im.ID)
		if href == "" {
			href = fmt.Sprintf("images/img%d", i)
		}
		imgTypes[href] = im.MediaType
		files["OEBPS/"+href] = im.Data
	}

	// NAV (EPUB3) / NCX (EPUB2).
	files["OEBPS/nav.xhtml"] = []byte(navXHTML(b, lang))
	files["OEBPS/toc.ncx"] = []byte(ncx(b, version, modified))

	// Контент.
	files["OEBPS/content.xhtml"] = []byte(contentXHTML(b, lang))
	files["OEBPS/style.css"] = []byte(defaultCSS)

	// OPF.
	files["OEBPS/content.opf"] = []byte(opf(b, lang, version, modified, imgTypes))

	for name, data := range files {
		if err := writeDeflated(zw, name, data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeStored пишет файл без сжатия (для mimetype).
func writeStored(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return fmt.Errorf("epub: %s: %w", name, err)
	}
	_, err = w.Write(data)
	return err
}

// writeDeflated пишет файл со сжатием.
func writeDeflated(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return fmt.Errorf("epub: %s: %w", name, err)
	}
	_, err = w.Write(data)
	return err
}

func containerXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`
}

func contentXHTML(b Book, lang string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="` + nsXHTML + `" lang="` + html.EscapeString(lang) + `" xml:lang="` + html.EscapeString(lang) + `">
<head>
  <meta charset="utf-8"/>
  <title>` + html.EscapeString(b.Title) + `</title>
  <link rel="stylesheet" type="text/css" href="style.css"/>
</head>
<body>
`)
	sb.WriteString(b.BodyHTML)
	sb.WriteString("\n</body>\n</html>\n")
	return sb.String()
}

func navXHTML(b Book, lang string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="` + nsXHTML + `" xmlns:epub="http://www.idpf.org/2007/ops" lang="` + html.EscapeString(lang) + `" xml:lang="` + html.EscapeString(lang) + `">
<head>
  <meta charset="utf-8"/>
  <title>Оглавление</title>
</head>
<body>
  <nav epub:type="toc" id="toc">
    <h1>Оглавление</h1>
    <ol>
`)
	for _, it := range b.TOC {
		if it.ID == "" || it.Title == "" {
			continue
		}
		sb.WriteString(`      <li><a href="content.xhtml#` + html.EscapeString(it.ID) + `">` + html.EscapeString(it.Title) + `</a></li>` + "\n")
	}
	if len(b.TOC) == 0 {
		sb.WriteString(`      <li><a href="content.xhtml">Начало</a></li>` + "\n")
	}
	sb.WriteString(`    </ol>
  </nav>
</body>
</html>
`)
	return sb.String()
}

func ncx(b Book, version string, modified time.Time) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="` + xmlEscape(b.Identifier) + `"/>
    <meta name="dtb:depth" content="3"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle><text>` + xmlEscape(b.Title) + `</text></docTitle>
  <navMap>
`)
	order := 0
	for _, it := range b.TOC {
		if it.ID == "" || it.Title == "" {
			continue
		}
		order++
		sb.WriteString(fmt.Sprintf(`    <navPoint id="np%d" playOrder="%d">
      <navLabel><text>%s</text></navLabel>
      <content src="content.xhtml#%s"/>
    </navPoint>
`, order, order, xmlEscape(it.Title), xmlEscape(it.ID)))
	}
	sb.WriteString(`  </navMap>
</ncx>
`)
	return sb.String()
}

func opf(b Book, lang, version string, modified time.Time, imgTypes map[string]string) string {
	var meta strings.Builder
	if version == "2.0" {
		// EPUB 2: обязателен cover/NCX; spine toc.
	} else {
		meta.WriteString(`    <meta property="dcterms:modified">` + modified.UTC().Format("2006-01-02T15:04:05Z") + `</meta>` + "\n")
	}
	var manifest strings.Builder
	manifest.WriteString(`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	manifest.WriteString(`    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	manifest.WriteString(`    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>` + "\n")
	manifest.WriteString(`    <item id="css" href="style.css" media-type="text/css"/>` + "\n")
	i := 0
	for href, mt := range imgTypes {
		i++
		if mt == "" {
			mt = mimeForExt(href)
		}
		manifest.WriteString(fmt.Sprintf(`    <item id="img%d" href="%s" media-type="%s"/>`+"\n", i, href, mt))
	}

	var authors strings.Builder
	for _, a := range b.Authors {
		authors.WriteString("    <dc:creator>" + xmlEscape(a) + "</dc:creator>\n")
	}
	var subjects strings.Builder
	for _, g := range b.Genres {
		subjects.WriteString("    <dc:subject>" + xmlEscape(g) + "</dc:subject>\n")
	}
	var annotation string
	if strings.TrimSpace(b.Annotation) != "" {
		annotation = "    <dc:description>" + xmlEscape(stripTags(b.Annotation)) + "</dc:description>\n"
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="%s" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="bookid">%s</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:language>%s</dc:language>
%s%s%s%s  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
    <itemref idref="content"/>
  </spine>
</package>
`, version, xmlEscape(b.Identifier), xmlEscape(b.Title), xmlEscape(lang),
		authors.String(), subjects.String(), annotation, meta.String(), manifest.String())
}

const defaultCSS = `body { font-family: serif; line-height: 1.5; margin: 1em; }
h1, h2, h3, h4, h5, h6 { font-family: sans-serif; line-height: 1.25; margin: 1.2em 0 0.5em; }
p { margin: 0 0 0.6em; text-indent: 1.2em; }
p:first-of-type, h1 + p, h2 + p { text-indent: 0; }
img.fb2-image { max-width: 100%; height: auto; display: block; margin: 0.5em auto; }
blockquote { margin: 0.5em 1.5em; font-style: italic; }
table { border-collapse: collapse; margin: 0.5em 0; }
table td, table th { border: 1px solid #999; padding: 2px 6px; }
.poem { margin: 0.5em 0 0.5em 1.5em; }
.epigraph { margin: 0.5em 2em; font-style: italic; }
`

// extForMime — расширение файла по MIME.
func extForMime(mt string) string {
	switch strings.ToLower(strings.TrimSpace(mt)) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func mimeForExt(href string) string {
	switch {
	case strings.HasSuffix(href, ".png"):
		return "image/png"
	case strings.HasSuffix(href, ".gif"):
		return "image/gif"
	case strings.HasSuffix(href, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(href, ".webp"):
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// ImageHref возвращает путь к файлу изображения (относительно OEBPS)
// для заданного id <binary>; совпадает с именем файла в архиве. Используется
// в fb2render.Options.ImageURL, чтобы ссылки в BodyHTML совпали с файлами.
func ImageHref(id string) string {
	name := sanitizeName(id)
	if name == "" {
		return ""
	}
	return "images/" + name
}

// sanitizeName оставляет в имени только безопасные символы.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}

// stripTags удаляет HTML-теги (упрощённо) для plain-text полей.
func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}
