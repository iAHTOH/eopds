package epub

import (
	"fmt"
	"strings"
)

// applyKoboSpans добавляет Kobo-разметку (koboSpan) в HTML-фрагмент книги.
//
// Kobo-читалка размечает текстовые узлы в <span class="koboSpan"
// id="kobo.P.N"> для корректной пагинации и закладок. Здесь содержимое
// каждого блочного элемента (<p>, <h1..h6>) оборачивается в один koboSpan;
// P — порядковый номер блока, N=1.
//
// Реализация — линейный сканер, без регулярных выражений с backreference
// (RE2 их не поддерживает) и без парсинга произвольного HTML. Опирается на
// то, что fb2render выдаёт парные НЕвложенные <p>/<hN> (HTML5: <p> не
// вкладывается в <p>). Вложенные <p> внутри <blockquote>/<section>
// обрабатываются корректно, т.к. ищем именно парный закрывающий тег.
func applyKoboSpans(body string) string {
	var sb strings.Builder
	para := 0
	i := 0
	for i < len(body) {
		start, tag := findBlockStart(body, i)
		if start < 0 {
			sb.WriteString(body[i:])
			break
		}
		openEnd := strings.IndexByte(body[start:], '>')
		if openEnd < 0 {
			sb.WriteString(body[i:])
			break
		}
		openEnd += start // позиция '>'
		openTag := body[start : openEnd+1]

		// Ищем парный закрывающий тег </tag>.
		closeStart := findBlockEnd(body, openEnd+1, tag)
		if closeStart < 0 {
			// Нет закрывающего — копируем как есть до конца.
			sb.WriteString(body[i:])
			break
		}
		inner := body[openEnd+1 : closeStart]
		closeTag := "</" + tag + ">"

		sb.WriteString(body[i:start])
		sb.WriteString(openTag)
		if koboWrap(inner) {
			para++
			fmt.Fprintf(&sb, `<span class="koboSpan" id="kobo.%d.1">`, para)
			sb.WriteString(inner)
			sb.WriteString("</span>")
		} else {
			sb.WriteString(inner)
		}
		sb.WriteString(closeTag)
		i = closeStart + len(closeTag)
	}
	return sb.String()
}

// koboWrap решает, надо ли оборачивать содержимое блока в koboSpan.
func koboWrap(inner string) bool {
	if strings.TrimSpace(inner) == "" {
		return false
	}
	if strings.Contains(inner, `class="koboSpan"`) {
		return false // уже размечено
	}
	t := strings.TrimSpace(inner)
	for _, prefix := range []string{"<img", "<table", "<svg", "<video", "<audio"} {
		if strings.HasPrefix(t, prefix) {
			return false
		}
	}
	return true
}

// findBlockStart ищет ближайший открывающий блочный тег (<p> или <h1..h6>) в
// s начиная с from. Возвращает позицию '<' и имя тега; -1, если не найдено.
func findBlockStart(s string, from int) (int, string) {
	for i := from; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		if i+1 >= len(s) {
			break
		}
		c := s[i+1]
		switch {
		case c == 'p':
			if isTagBoundary(s, i+2) {
				return i, "p"
			}
		case c == 'h' && i+2 < len(s) && s[i+2] >= '1' && s[i+2] <= '6':
			if isTagBoundary(s, i+3) {
				return i, s[i+1 : i+3]
			}
		}
	}
	return -1, ""
}

// isTagBoundary проверяет, что за именем тега идёт '>', '/' или пробельный
// символ (а не буква/цифра: чтобы не спутать <p> с <pre>).
func isTagBoundary(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	c := s[pos]
	return c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// findBlockEnd ищет закрывающий тег </tag> начиная с from.
func findBlockEnd(s string, from int, tag string) int {
	needle := "</" + tag
	for {
		if from > len(s) {
			return -1
		}
		idx := strings.Index(s[from:], needle)
		if idx < 0 {
			return -1
		}
		abs := from + idx
		after := abs + len(needle)
		if after < len(s) && (s[after] == '>' || s[after] == ' ' || s[after] == '\t') {
			return abs
		}
		from = abs + len(needle)
	}
}

// koboJS — минимальный скрипт-заглушка Kobo (наличие файла ожидается
// читалкой; реальная пагинация Kobo не требуется для корректного отображения).
const koboJS = `(function(){
  function kobo(){
    var spans = document.querySelectorAll('span.koboSpan');
    if(!spans || !spans.length) return;
  }
  if (document.readyState !== 'loading') { kobo(); }
  else { document.addEventListener('DOMContentLoaded', kobo); }
})();
`

// koboCSS — базовые стили Kobo.
const koboCSS = `.koboSpan { -webkit-text-size-adjust: 100%; }
body { orphans: 2; widows: 2; }
`
