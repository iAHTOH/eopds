package fb2render

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
)

const fb2Template = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
 <description><title-info><book-title>Книга</book-title></title-info></description>
 %s
</FictionBook>`

// xmlDecoderForTest оборачивает HTML-фрагмент в корневой элемент, чтобы
// проверить его как well-formed XML.
func xmlDecoderForTest(fragment string) *xml.Decoder {
	return xml.NewDecoder(strings.NewReader("<root>" + fragment + "</root>"))
}

func renderBody(t *testing.T, body string, opt Options) Result {
	t.Helper()
	res, err := Render(strings.NewReader(fmt.Sprintf(fb2Template, body)), opt)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return res
}

func TestRenderParagraphsAndInline(t *testing.T) {
	body := `<body><section><title><p>Глава 1</p></title>
	    <p>Привет, <strong>мир</strong> и <emphasis>курсив</emphasis>.</p>
	    <p>А &lt;это&gt; — «кавычки».</p></section></body>`
	res := renderBody(t, body, Options{})
	for _, want := range []string{
		`<h2 class="fb2-title">Глава 1</h2>`,
		`Привет, <strong>мир</strong> и <em>курсив</em>.`,
		`А &lt;это&gt; — «кавычки».`,
	} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("нет фрагмента %q в:\n%s", want, res.HTML)
		}
	}
}

func TestRenderTOC(t *testing.T) {
	body := `<body>
	    <section><title><p>Часть 1</p></title>
	        <section><title><p>Глава 1</p></title><p>Текст.</p></section>
	        <section><title><p>Глава 2</p></title><p>Текст.</p></section>
	    </section></body>`
	res := renderBody(t, body, Options{})
	if len(res.TOC) != 3 {
		t.Fatalf("TOC: ожидалось 3, получено %d: %+v", len(res.TOC), res.TOC)
	}
	if res.TOC[0].Title != "Часть 1" || res.TOC[0].Level != 0 {
		t.Errorf("TOC[0] = %+v", res.TOC[0])
	}
	if res.TOC[1].Title != "Глава 1" || res.TOC[1].Level != 1 {
		t.Errorf("TOC[1] = %+v", res.TOC[1])
	}
}

func TestRenderPoem(t *testing.T) {
	body := `<body><section><title><p>Стих</p></title>
	    <poem>
	        <stanza><v>Строка один,</v><v>строка два.</v></stanza>
	        <text-author>Поэт</text-author>
	    </poem></section></body>`
	res := renderBody(t, body, Options{})
	for _, want := range []string{
		`<div class="fb2-poem">`,
		`<div class="fb2-stanza">`,
		`<p class="fb2-v">Строка один,</p>`,
		`<p class="fb2-author">Поэт</p>`,
	} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("нет %q в:\n%s", want, res.HTML)
		}
	}
}

func TestRenderNotes(t *testing.T) {
	body := `<body>
	    <section><title><p>Глава</p></title><p>Текст<a l:href="#n_1" type="note">[1]</a>.</p></section>
	  </body>
	  <body name="notes">
	    <title><p>Примечания</p></title>
	    <section id="n_1"><title><p>1</p></title><p>Сноска.</p></section>
	  </body>`
	res := renderBody(t, body, Options{WithNotes: true})
	for _, want := range []string{
		`<a class="fb2-note-ref" href="#n_1">[1]</a>`,
		`<section class="fb2-notes">`,
		`<aside class="fb2-note" id="n_1">`,
		`Сноска.`,
	} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("нет %q в:\n%s", want, res.HTML)
		}
	}
	// Без WithNotes блок примечаний не выводится.
	res2 := renderBody(t, body, Options{})
	if strings.Contains(res2.HTML, "fb2-notes") {
		t.Errorf("блок примечаний не должен выводиться без WithNotes")
	}
}

func TestRenderImageDataURI(t *testing.T) {
	body := `<body><section><title><p>Гл</p></title>
	    <image l:href="#pic"/>
	    <p>Текст.</p></section>
	  </body>
	  <binary id="pic" content-type="image/png">aGVsbG8=</binary>`
	res := renderBody(t, body, Options{})
	if !strings.Contains(res.HTML, `src="data:image/png;base64,aGVsbG8="`) {
		t.Errorf("нет data URI картинки:\n%s", res.HTML)
	}
}

func TestRenderImageURLCallback(t *testing.T) {
	body := `<body><section><title><p>Гл</p></title><image l:href="#pic"/></section></body>`
	res := renderBody(t, body, Options{ImageURL: func(id string) string { return "/img/" + id }})
	if !strings.Contains(res.HTML, `src="/img/pic"`) {
		t.Errorf("нет URL из колбэка:\n%s", res.HTML)
	}
}

func TestRenderTable(t *testing.T) {
	body := `<body><section><title><p>Т</p></title>
	    <table><tr><th>А</th><th>Б</th></tr><tr><td>1</td><td>2</td></tr></table></section></body>`
	res := renderBody(t, body, Options{})
	for _, want := range []string{`<table class="fb2-table">`, `<th>А</th>`, `<td>2</td>`} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("нет %q в:\n%s", want, res.HTML)
		}
	}
}

func TestRenderRejectsNonFB2(t *testing.T) {
	_, err := Render(strings.NewReader(`<?xml version="1.0"?><html><body>x</body></html>`), Options{})
	if err == nil {
		t.Fatal("ожидалась ошибка ErrNotFB2")
	}
}

// TestRenderXHTMLVoidElements — тело должно быть XHTML-совместимым (валидный
// XML): void-элементы (<br/>, <img/>) самозакрываются, иначе EPUB отвергнет
// content.xhtml. Заголовок из нескольких <p> даёт <br/>.
func TestRenderXHTMLVoidElements(t *testing.T) {
	res := renderBody(t, `<body><section><title><p>Строка 1</p><p>Строка 2</p></title><p>Текст</p></section></body>`, Options{})
	if strings.Contains(res.HTML, "<br>") {
		t.Fatalf("незакрытый <br>: %s", res.HTML)
	}
	if !strings.Contains(res.HTML, "<br/>") {
		t.Fatalf("ожидался <br/> в заголовке: %s", res.HTML)
	}
	// Всё тело должно быть well-formed XML.
	dec := xmlDecoderForTest(res.HTML)
	for {
		_, err := dec.Token()
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("тело не является well-formed XML: %v\n%s", err, res.HTML)
		}
	}
}
