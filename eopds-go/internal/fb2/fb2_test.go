package fb2

import (
	"strings"
	"testing"
)

func TestParseAnnotationNested(t *testing.T) {
	// Аннотация с вложенными <p>, <emphasis>, <empty-line/> — реальная структура FB2.
	const doc = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
 <description>
  <title-info>
   <genre>sf</genre>
   <author><first-name>Иван</first-name><last-name>Петров</last-name></author>
   <book-title>Тестовая книга</book-title>
   <annotation>
    <p>Первый абзац <emphasis>с выделением</emphasis>.</p>
    <empty-line/>
    <p>Второй абзац.</p>
   </annotation>
   <lang>ru</lang>
  </title-info>
 </description>
 <body><section><p>Текст книги, который не должен попасть в аннотацию.</p></section></body>
</FictionBook>`

	bd, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ann := bd.Annotation
	if !strings.Contains(ann, "Первый абзац") {
		t.Errorf("annotation missing first paragraph: %q", ann)
	}
	if !strings.Contains(ann, "с выделением") {
		t.Errorf("annotation missing emphasis text: %q", ann)
	}
	if !strings.Contains(ann, "Второй абзац") {
		t.Errorf("annotation missing second paragraph: %q", ann)
	}
	if strings.Contains(ann, "не должен попасть") {
		t.Errorf("annotation leaked body text: %q", ann)
	}
	if bd.Title != "Тестовая книга" {
		t.Errorf("title = %q", bd.Title)
	}
	if len(bd.Authors) != 1 || bd.Authors[0] != "Иван Петров" {
		t.Errorf("authors = %v", bd.Authors)
	}
}
