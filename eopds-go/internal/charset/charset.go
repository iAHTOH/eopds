// Package charset реализует минимальную поддержку декодирования
// однобайтовых кириллических кодировок в UTF-8 для FB2-файлов.
//
// Многие файлы в библиотеках flibusta сохранены в windows-1251 (cp1251),
// а не в UTF-8. Go-стандартная библиотека encoding/xml сама по себе
// не умеет конвертировать кодировки, поэтому мы перехватываем объявление
// кодировки через Decoder.CharsetReader и подставляем транслирующий reader.
//
// Чистый stdlib: таблица cp1251 зашита прямо здесь, без golang.org/x/text.
package charset

import (
	"io"
	"strings"
	"unicode/utf8"
)

// cp1251High — отображение байтов 0x80..0xFF в кодовые точки Unicode.
// 0x00..0x7F совпадают с ASCII. Таблица соответствует Windows-1251.
var cp1251High = [128]rune{
	0x0402, 0x0403, 0x201A, 0x0453, 0x201E, 0x2026, 0x2020, 0x2021,
	0x20AC, 0x2030, 0x0409, 0x2039, 0x040A, 0x040C, 0x040B, 0x040F,
	0x0452, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0xFFFD, 0x2122, 0x0459, 0x203A, 0x045A, 0x045C, 0x045B, 0x045F,
	0x00A0, 0x040E, 0x045E, 0x0408, 0x00A4, 0x0490, 0x00A6, 0x00A7,
	0x0401, 0x00A9, 0x0404, 0x00AB, 0x00AC, 0x00AD, 0x00AE, 0x0407,
	0x00B0, 0x00B1, 0x0406, 0x0456, 0x0491, 0x00B5, 0x00B6, 0x00B7,
	0x0451, 0x2116, 0x0454, 0x00BB, 0x0458, 0x0405, 0x0455, 0x0457,
	0x0410, 0x0411, 0x0412, 0x0413, 0x0414, 0x0415, 0x0416, 0x0417,
	0x0418, 0x0419, 0x041A, 0x041B, 0x041C, 0x041D, 0x041E, 0x041F,
	0x0420, 0x0421, 0x0422, 0x0423, 0x0424, 0x0425, 0x0426, 0x0427,
	0x0428, 0x0429, 0x042A, 0x042B, 0x042C, 0x042D, 0x042E, 0x042F,
	0x0430, 0x0431, 0x0432, 0x0433, 0x0434, 0x0435, 0x0436, 0x0437,
	0x0438, 0x0439, 0x043A, 0x043B, 0x043C, 0x043D, 0x043E, 0x043F,
	0x0440, 0x0441, 0x0442, 0x0443, 0x0444, 0x0445, 0x0446, 0x0447,
	0x0448, 0x0449, 0x044A, 0x044B, 0x044C, 0x044D, 0x044E, 0x044F,
}

// Reader преобразует поток байтов из указанной однобайтовой кодировки
// в UTF-8. Возвращает исходный reader, если кодировка не распознана
// (в этом случае содержимое читается как есть — «терпимое» поведение).
//
// Реализация простая и небуферизованная по потоку: читаем всё имеющееся
// и транслируем. Для FB2-файлов (метаданные в начале файла) этого достаточно,
// а объём метаданных невелик.
func Reader(label string, input io.Reader) (io.Reader, error) {
	enc := normalizeLabel(label)
	switch enc {
	case "windows-1251", "cp1251", "win-1251", "x-cp1251":
		return &cp1251Reader{src: input}, nil
	case "utf-8", "utf8", "us-ascii", "ascii", "":
		return input, nil
	default:
		// Неизвестная кодировка — читаем как есть (best effort).
		return input, nil
	}
}

// cp1251Reader — потоковый транскодер cp1251 -> UTF-8.
// Читает «сырые» байты из src и отдаёт уже UTF-8.
// Работает инкрементально (буфер на запрос), поэтому не требует
// загрузки всего файла в память — важно для больших FB2.
type cp1251Reader struct {
	src io.Reader
	buf []byte // остаток неотданных UTF-8 байт
	err error
}

func (r *cp1251Reader) Read(p []byte) (int, error) {
	// Сначала отдаём то, что осталось с прошлого раза.
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	if r.err != nil {
		return 0, r.err
	}

	// Читаем порцию исходных (однобайтовых) данных и транслируем.
	// На 1 исходный байт — максимум 2 байта UTF-8 для cp1251.
	const chunk = 4096
	raw := make([]byte, chunk)
	n, err := r.src.Read(raw)
	if n > 0 {
		out := toUTF8(raw[:n])
		if len(out) > 0 {
			// Помещаем в буфер; отдаём в p столько, сколько влезет.
			r.buf = out
			c := copy(p, r.buf)
			r.buf = r.buf[c:]
			if err == io.EOF {
				// Отложим EOF до следующего вызова, когда буфер опустеет.
				if len(r.buf) > 0 {
					return c, nil
				}
				r.err = io.EOF
				return c, nil
			}
			return c, nil
		}
	}
	if err != nil {
		r.err = err
		return 0, err
	}
	return 0, nil
}

func normalizeLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

// toUTF8 транслирует однобайтовый cp1251 в UTF-8.
// Байты 0x00..0x7F копируются как есть; 0x80..0xFF — через таблицу.
func toUTF8(in []byte) []byte {
	out := make([]byte, 0, len(in)*2)
	var buf [utf8.UTFMax]byte
	for _, b := range in {
		if b < 0x80 {
			out = append(out, b)
			continue
		}
		r := cp1251High[b-0x80]
		n := utf8.EncodeRune(buf[:], r)
		out = append(out, buf[:n]...)
	}
	return out
}
