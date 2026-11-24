package fb2

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/charset"
)

// ErrNoCover — в файле нет обложки (нет <coverpage> или <binary>).
var ErrNoCover = errors.New("fb2: no cover")

// Cover — извлечённая обложка.
type Cover struct {
	Data        []byte // декодированные байты изображения
	ContentType string // image/jpeg, image/png, ...
}

// ExtractedExt возвращает расширение файла по Content-Type (без точки).
func (c Cover) Ext() string {
	switch strings.ToLower(c.ContentType) {
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "jpg"
	}
}

// ExtractCover читает FB2 и возвращает обложку (первую, на которую ссылается
// <coverpage><image l:href="#id">). Бинарники (<binary>) хранятся в base64.
//
// Извлечение требует чтения всего файла (binary в конце документа), поэтому
// вызывать стоит лениво — только когда обложка реально нужна.
func ExtractCover(r io.Reader) (Cover, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = charset.Reader

	var coverID string
	// binary накапливается построчно в CharData (base64 может быть разделён).
	type binaryBuf struct {
		contentType string
		b64         strings.Builder
	}
	binaries := make(map[string]*binaryBuf)

	var (
		curBinaryID string
		inCoverpage bool
		inBinary    bool
		foundRoot   bool
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !foundRoot {
				return Cover{}, err
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !foundRoot {
				if name != "fictionbook" {
					return Cover{}, ErrNotFB2
				}
				foundRoot = true
			}
			switch name {
			case "coverpage":
				inCoverpage = true
			case "image":
				if inCoverpage && coverID == "" {
					for _, a := range t.Attr {
						ln := strings.ToLower(a.Name.Local)
						if ln == "href" { // l:href → Local="href"
							coverID = strings.TrimPrefix(strings.TrimSpace(a.Value), "#")
						}
					}
				}
			case "binary":
				inBinary = true
				curBinaryID = ""
				b := &binaryBuf{}
				for _, a := range t.Attr {
					switch strings.ToLower(a.Name.Local) {
					case "id":
						curBinaryID = strings.TrimSpace(a.Value)
					case "content-type":
						b.contentType = strings.TrimSpace(a.Value)
					}
				}
				if curBinaryID != "" {
					binaries[curBinaryID] = b
				}
			}
		case xml.CharData:
			if inBinary && curBinaryID != "" {
				if b := binaries[curBinaryID]; b != nil {
					b.b64.Write(t)
				}
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			switch name {
			case "coverpage":
				inCoverpage = false
			case "binary":
				inBinary = false
				curBinaryID = ""
			}
		}
	}

	if coverID == "" {
		return Cover{}, ErrNoCover
	}
	b := binaries[coverID]
	if b == nil || b.b64.Len() == 0 {
		return Cover{}, ErrNoCover
	}
	// Убираем пробелы/переводы строк из base64.
	raw := strings.Join(strings.Fields(b.b64.String()), "")
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return Cover{}, err
	}
	ct := b.contentType
	if ct == "" {
		ct = "image/jpeg"
	}
	return Cover{Data: data, ContentType: ct}, nil
}
