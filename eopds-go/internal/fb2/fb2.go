// Package fb2 — минимальный, устойчивый к ошибкам парсер метаданных FB2.
//
// FB2 — это XML. Нас интересует в первую очередь узел <description><title-info>:
//
//	<book-title>, <author> (first-name/last-name/middle-name),
//	<annotation>, <genre>, <lang>, <date>, <sequence name="..." number="...">
//
// Парсер намеренно «терпимый»: если файл частично битый, мы всё равно
// пытаемся вытащить то, что успели прочитать, и не падаем.
// Используем потоковый encoding/xml.Decoder, а не загрузку всего дерева.
package fb2

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/charset"
	"github.com/iAHTOH/eopds/eopds-go/internal/meta"
)

// ErrNotFB2 возвращается, если корневой элемент не <FictionBook>.
var ErrNotFB2 = errors.New("not a FictionBook")

// bookMeta — промежуточная структура для накопления полей.
type result struct {
	title      string
	lang       string
	docdate    string
	annotation string
	genres     []string
	authors    []string
	series     *meta.Series
}

// Parse читает FB2 из r и возвращает метаданные.
//
// Возвращает ErrNotFB2, если это не FB2. Иные ошибки XML не фатальны —
// возвращается то, что удалось распарсить (или ошибку, если вообще ничего).
func Parse(r io.Reader) (meta.BookData, error) {
	dec := xml.NewDecoder(r)
	// Разрешаем работу с картинками/бинарниками как с текстом — они нас
	// не интересуют, но не должны валить декодер.
	dec.Strict = false
	// Поддержка кириллических кодировок (windows-1251 и др.).
	dec.CharsetReader = charset.Reader

	res := &result{}
	foundRoot := false
	finished := false

	// Текущий контекст пути — отслеживаем, где находимся.
	var stack []string

	// Промежуточные накопители для текущего автора.
	var curAuthorFirst, curAuthorLast, curAuthorMiddle string
	inAuthor := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Ошибка XML — не фатальна: забираем то, что успели.
			if !foundRoot {
				return meta.BookData{}, err
			}
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			stack = append(stack, name)
			if !foundRoot {
				if name == "fictionbook" {
					foundRoot = true
				} else {
					// Это не FB2 (первый значимый элемент не FictionBook).
					return meta.BookData{}, ErrNotFB2
				}
			}
			switch name {
			case "author":
				// Автор встречается и в title-info, и в document-info.
				// Берём только из title-info, т.е. когда в стеке есть title-info.
				if inPath(stack, "title-info") {
					inAuthor = true
					curAuthorFirst, curAuthorLast, curAuthorMiddle = "", "", ""
				}
			case "sequence":
				// Как и в Python-парсере (fb2.py), берём ТОЛЬКО ПЕРВУЮ <sequence>
				// из title-info. В FB2 sequence может быть вложенным (серия ->
				// подсерия), и Python использует внешнюю (первую).
				if inPath(stack, "title-info") && res.series == nil {
					var s meta.Series
					for _, a := range t.Attr {
						switch strings.ToLower(a.Name.Local) {
						case "name":
							s.Title = strings.TrimSpace(a.Value)
						case "number":
							s.Index = atoiSafe(a.Value)
						}
					}
					if s.Title != "" {
						res.series = &s
					}
				}
			case "date":
				// В Python docdate берётся из document-info/date по атрибуту
				// value (приоритет) или тексту. Запоминаем атрибут value.
				if inPath(stack, "document-info") && res.docdate == "" {
					for _, a := range t.Attr {
						if strings.ToLower(a.Name.Local) == "value" {
							if v := strings.TrimSpace(a.Value); v != "" {
								res.docdate = v
							}
						}
					}
				}
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			cur := stack[len(stack)-1]
			switch cur {
			case "book-title":
				if inPath(stack, "title-info") && res.title == "" {
					res.title = joinText(res.title, text)
				}
			case "lang":
				if inPath(stack, "title-info") && res.lang == "" {
					res.lang = text
				}
			case "date":
				// Текстовое значение даты документа (fallback, если нет @value).
				if inPath(stack, "document-info") && res.docdate == "" {
					res.docdate = text
				}
			case "annotation":
				if inPath(stack, "title-info") {
					res.annotation = joinText(res.annotation, text)
				}
			case "genre":
				if inPath(stack, "title-info") {
					res.genres = append(res.genres, text)
				}
			case "first-name":
				if inAuthor {
					curAuthorFirst = text
				}
			case "last-name":
				if inAuthor {
					curAuthorLast = text
				}
			case "middle-name":
				if inAuthor {
					curAuthorMiddle = text
				}
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "author" && inAuthor {
				inAuthor = false
				full := buildAuthorName(curAuthorFirst, curAuthorLast, curAuthorMiddle)
				if full != "" {
					res.authors = append(res.authors, full)
				}
			}
			// Метаданные живут только в <description>. Как только он закрыт,
			// дальше идёт тело книги (крупное) — прекращаем разбор для скорости.
			if name == "description" {
				// Снимаем со стека и выходим из цикла.
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				finished = true
			}
			// Снимаем со стека последний элемент.
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		if finished {
			break
		}
	}

	if !foundRoot {
		return meta.BookData{}, ErrNotFB2
	}

	return meta.BookData{
		Title:      res.title,
		Lang:       res.lang,
		DocDate:    res.docdate,
		Annotation: res.annotation,
		Genres:     dedup(res.genres),
		Authors:    res.authors,
		Series:     res.series,
	}, nil
}

// buildAuthorName собирает имя автора так же, как Python-парсер (fb2.py):
// ' '.join([first_name, last_name]) — т.е. сначала имя, затем фамилия.
// Python-сканер затем (в eopdscan.py) переносит последнее слово в начало,
// если в строке нет запятой. Повторяем этот формат, чтобы результат
// совпадал с уже проиндексированной БД.
//
// middle-name Python не использует — тоже не используем.
func buildAuthorName(first, last, middle string) string {
	if first == "" && last == "" {
		return ""
	}
	if first == "" {
		return last
	}
	if last == "" {
		return first
	}
	return first + " " + last
}

// inPath проверяет, присутствует ли элемент name в текущем стеке пути.
func inPath(stack []string, name string) bool {
	for _, s := range stack {
		if s == name {
			return true
		}
	}
	return false
}

func joinText(acc, add string) string {
	if acc == "" {
		return add
	}
	return acc + " " + add
}

func dedup(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

func atoiSafe(s string) int {
	n := 0
	neg := false
	s = strings.TrimSpace(s)
	for i, c := range s {
		if i == 0 && (c == '-' || c == '+') {
			neg = c == '-'
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}
