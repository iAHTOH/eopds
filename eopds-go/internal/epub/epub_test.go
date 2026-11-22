package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func sampleBook() Book {
	return Book{
		Title:      "Тестовая книга",
		Lang:       "ru",
		Authors:    []string{"Иван Петров"},
		Genres:     []string{"sf"},
		Annotation: "Описание <b>книги</b>",
		Identifier: "urn:eopds:book:1",
		Modified:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		BodyHTML:   `<h1 id="s1">Глава 1</h1><p>Текст <img class="fb2-image" src="images/pic1" alt=""/></p>`,
		TOC:        []TOCItem{{ID: "s1", Title: "Глава 1", Level: 0}},
		Images:     []Image{{ID: "pic1", MediaType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff, 0xd9}}},
	}
}

func openZip(t *testing.T, data []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return zr
}

func readFile(t *testing.T, zr *zip.Reader, name string) (string, *zip.File) {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b), f
		}
	}
	t.Fatalf("файл не найден в архиве: %s", name)
	return "", nil
}

func TestBuildEPUB3Structure(t *testing.T) {
	data, err := Build(sampleBook(), Options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	zr := openZip(t, data)

	// mimetype должен быть первым и без сжатия.
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Fatalf("первый файл архива должен быть mimetype")
	}
	if zr.File[0].Method != zip.Store {
		t.Fatalf("mimetype должен быть без сжатия (Store)")
	}
	mt, _ := readFile(t, zr, "mimetype")
	if mt != "application/epub+zip" {
		t.Fatalf("mimetype = %q", mt)
	}

	// Обязательные файлы.
	for _, name := range []string{
		"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml",
		"OEBPS/toc.ncx", "OEBPS/content.xhtml", "OEBPS/style.css",
		"OEBPS/images/pic1",
	} {
		readFile(t, zr, name)
	}

	// XHTML/OPF/NCX должны быть валидным XML.
	for _, name := range []string{"OEBPS/content.xhtml", "OEBPS/nav.xhtml", "OEBPS/toc.ncx", "OEBPS/content.opf"} {
		content, _ := readFile(t, zr, name)
		dec := xml.NewDecoder(strings.NewReader(content))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: невалидный XML: %v", name, err)
			}
		}
	}

	// Экранирование в OPF.
	opf, _ := readFile(t, zr, "OEBPS/content.opf")
	if !strings.Contains(opf, "Тестовая книга") {
		t.Fatalf("в OPF нет названия")
	}
	if strings.Contains(opf, "<b>книги</b>") {
		t.Fatalf("аннотация не должна содержать теги: %s", opf)
	}
	if !strings.Contains(opf, "2026-01-01T00:00:00Z") {
		t.Fatalf("в OPF нет dcterms:modified")
	}
}

func TestBuildEPUB2(t *testing.T) {
	data, err := Build(sampleBook(), Options{Version: "2.0"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	zr := openZip(t, data)
	opf, _ := readFile(t, zr, "OEBPS/content.opf")
	if !strings.Contains(opf, `version="2.0"`) {
		t.Fatalf("ожидалась версия 2.0 в OPF: %s", opf)
	}
	// NCX есть.
	readFile(t, zr, "OEBPS/toc.ncx")
}

func TestExtAndMime(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":    ".jpg",
		"image/png":     ".png",
		"image/gif":     ".gif",
		"image/svg+xml": ".svg",
	}
	for mt, ext := range cases {
		if got := extForMime(mt); got != ext {
			t.Errorf("extForMime(%q)=%q want %q", mt, got, ext)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName("a/b c#d.webp"); got != "abcdwebp" {
		t.Errorf("sanitizeName = %q", got)
	}
}
