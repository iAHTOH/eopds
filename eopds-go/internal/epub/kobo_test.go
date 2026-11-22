package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestApplyKoboSpans(t *testing.T) {
	in := `<h2 class="fb2-title">Автор<br/>Название</h2>` +
		`<section class="fb2-section" id="s1">` +
		`<p>Первый абзац.</p>` +
		`<p></p>` +
		`<p><img class="fb2-image" src="images/a" alt=""/></p>` +
		`<blockquote><p>Цитата.</p></blockquote>` +
		`<h3 id="x">Подзаголовок</h3>` +
		`<p>Второй <strong>абзац</strong>.</p>` +
		`</section>`
	out := applyKoboSpans(in)

	// Ожидаем span вокруг текста: заголовок h2, абзац 1, цитата, h3, абзац 2.
	if got := strings.Count(out, `class="koboSpan"`); got != 5 {
		t.Fatalf("ожидалось 5 koboSpan, получено %d:\n%s", got, out)
	}
	// Последовательные id.
	for _, id := range []string{"kobo.1.1", "kobo.2.1", "kobo.3.1", "kobo.4.1", "kobo.5.1"} {
		if !strings.Contains(out, `id="`+id+`"`) {
			t.Fatalf("нет id=%s:\n%s", id, out)
		}
	}
	// Пустой <p></p> и <p><img.../></p> не обёрнуты.
	if strings.Contains(out, `<p><span class="koboSpan" id="kobo.2.1"><img`) {
		t.Fatalf("картинка не должна оборачиваться: %s", out)
	}
	// Идемпотентность: повторный прогон не добавляет новых span.
	out2 := applyKoboSpans(out)
	if strings.Count(out2, `class="koboSpan"`) != 5 {
		t.Fatalf("повторная обработка задвоила span: %d", strings.Count(out2, `class="koboSpan"`))
	}
}

func TestApplyKoboSpansNoPre(t *testing.T) {
	// <pre>/<progress> не должны считаться блочными.
	in := `<pre>код</pre><p>текст</p>`
	out := applyKoboSpans(in)
	if strings.Contains(out, `<span class="koboSpan" id="kobo.1.1">код`) {
		t.Fatalf("<pre> ошибочно обёрнут: %s", out)
	}
	if !strings.Contains(out, `<span class="koboSpan" id="kobo.1.1">текст`) {
		t.Fatalf("<p> должен быть обёрнут: %s", out)
	}
}

func TestBuildKepub(t *testing.T) {
	b := sampleBook()
	data, err := Build(b, Options{Version: "3.0", Kobo: true})
	if err != nil {
		t.Fatalf("build kepub: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	get := func(name string) string {
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open()
				defer rc.Close()
				bb, _ := io.ReadAll(rc)
				return string(bb)
			}
		}
		t.Fatalf("нет файла %s", name)
		return ""
	}
	// kobo.js / kobo.css присутствуют.
	get("OEBPS/kobo.js")
	get("OEBPS/kobo.css")

	content := get("OEBPS/content.xhtml")
	if !strings.Contains(content, `class="koboSpan"`) {
		t.Fatalf("в content.xhtml нет koboSpan: %s", content)
	}
	if !strings.Contains(content, `href="kobo.css"`) || !strings.Contains(content, `src="kobo.js"`) {
		t.Fatalf("в <head> нет kobo.css/kobo.js")
	}
	// content.xhtml остаётся well-formed XML.
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("content.xhtml невалиден после koboSpan: %v", err)
		}
	}
	// OPF содержит ссылки на kobo-ресурсы.
	opf := get("OEBPS/content.opf")
	if !strings.Contains(opf, `href="kobo.js"`) || !strings.Contains(opf, `href="kobo.css"`) {
		t.Fatalf("в OPF нет kobo-ресурсов")
	}
}

func TestBuildKoboIgnoredForEPUB2(t *testing.T) {
	data, err := Build(sampleBook(), Options{Version: "2.0", Kobo: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		if f.Name == "OEBPS/kobo.js" {
			t.Fatalf("kepub-разметка не должна применяться к EPUB2")
		}
	}
}
