// Package fb2render преобразует тело FB2 (<body>) в безопасный HTML для
// онлайн-читалки.
//
// Парсер потоковый и «терпимый» (как internal/fb2): битые места не валят
// рендер, а по возможности пропускаются. Поддерживаются основные элементы
// FB2: section, title, subtitle, p, strong, emphasis, style, epigraph,
// poem/stanza/v, text-author, cite, empty-line, table/tr/td/th, image
// (берётся из <binary> и встраивается как data URI), сноски
// (<a type="note"> → ссылка на <body name="notes">).
//
// Весь текстовый контент экранируется (html.EscapeString), поэтому
// результат безопасно вставлять в страницу.
package fb2render

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"sort"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/charset"
)

// ErrNotFB2 возвращается, если корневой элемент не <FictionBook>.
var ErrNotFB2 = errors.New("not a FictionBook")

// TOCItem — пункт оглавления (заголовок секции первого/второго уровня).
type TOCItem struct {
	ID    string
	Title string
	Level int
}

// Image — встроенная в FB2 картинка (для выноса в файлы, например в EPUB).
type Image struct {
	// ID — идентификатор <binary> без '#' (на него ссылается <image href>).
	ID string
	// MediaType — MIME-тип (image/jpeg, image/png, ...).
	MediaType string
	// Data — декодированные байты изображения.
	Data []byte
}

// Result — результат рендера тела книги.
type Result struct {
	// HTML — тело книги в виде HTML-фрагмента (без <html>/<body>).
	HTML string
	// TOC — оглавление (в порядке следования).
	TOC []TOCItem
	// Images — встроенные картинки (в порядке <binary>).
	Images []Image
}

// Options управляют рендером.
type Options struct {
	// ImageURL, если задан, используется для построения адреса картинки
	// по её id вместо встраивания data URI. Например, для
	// func(id string) string { return "/library/book/123/img/" + id }.
	// Если nil — картинки встраиваются как data:<mime>;base64,...
	ImageURL func(id string) string
	// WithNotes — включать ли блок примечаний (<body name="notes">) в конце.
	WithNotes bool
}

// Render читает FB2 из r и возвращает отрендеренное тело и оглавление.
// Возвращает ErrNotFB2, если это не FB2.
func Render(r io.Reader, opt Options) (Result, error) {
	b, err := parse(r)
	if err != nil {
		return Result{}, err
	}
	return b.render(opt)
}

// ---------------------------------------------------------------------------
// Внутреннее представление документа.
//
// Важно сохранять порядок смешанного контента: «текст <strong>x</strong>
// текст» — текст до, элемент и текст после идут по порядку. Поэтому каждый
// узел хранит последовательность item (текст ИЛИ дочерний элемент).
// ---------------------------------------------------------------------------

// item — элемент упорядоченного контента: либо текст, либо дочерний узел.
type item struct {
	text string
	elem *node
}

// node — упрощённое дерево FB2.
type node struct {
	name    string
	attrs   map[string]string
	content []item
}

// binary — встроенная картинка (<binary>).
type binary struct {
	id        string
	mediaType string // content-type
	data      string // base64 (как в исходнике)
}

// doc — разобранный FB2.
type doc struct {
	body  *node // первое <body> (основной текст)
	notes *node // <body name="notes"> (примечания), если есть
	bin   map[string]*binary
}

func parse(r io.Reader) (*doc, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	// Поддержка кириллических кодировок (windows-1251 и др.).
	dec.CharsetReader = charset.Reader

	var (
		allBodies []*node
		allBin    = map[string]*binary{}
		stack     []*node // путь от корня (корень не входит)
		foundRoot bool
	)

	// appendItem добавляет текст/элемент в текущий контейнер.
	appendItem := func(it item) {
		if len(stack) == 0 {
			return
		}
		n := stack[len(stack)-1]
		// Сливаем соседние текстовые куски.
		if it.elem == nil && len(n.content) > 0 {
			last := &n.content[len(n.content)-1]
			if last.elem == nil {
				last.text += it.text
				return
			}
		}
		n.content = append(n.content, it)
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Терпимый режим: останавливаемся, но используем то, что есть.
			if !foundRoot {
				return nil, err
			}
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := localName(t.Name.Local)
			if !foundRoot {
				// Корень может быть FictionBook в разном регистре.
				if !strings.EqualFold(name, "fictionbook") {
					return nil, ErrNotFB2
				}
				foundRoot = true
				continue
			}
			// <binary> содержит большой base64-блок: читаем целиком и
			// сохраняем отдельно, в дерево не добавляем.
			if name == "binary" {
				var data string
				if err := dec.DecodeElement(&data, &t); err != nil {
					continue // битый binary не критичен
				}
				id := attrsMap(t.Attr)["id"]
				mt := attrsMap(t.Attr)["content-type"]
				if id != "" {
					allBin[id] = &binary{id: id, mediaType: mt, data: strings.TrimSpace(data)}
				}
				continue
			}
			n := &node{name: name, attrs: attrsMap(t.Attr)}
			appendItem(item{elem: n})
			if name == "body" {
				allBodies = append(allBodies, n)
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			if s := string(t); s != "" {
				appendItem(item{text: s})
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			stack = stack[:len(stack)-1]
		}
	}

	if !foundRoot {
		return nil, ErrNotFB2
	}

	d := &doc{bin: allBin}
	for _, b := range allBodies {
		if strings.EqualFold(b.attrs["name"], "notes") {
			if d.notes == nil {
				d.notes = b
			}
			continue
		}
		if d.body == nil {
			d.body = b
		}
	}
	if d.body == nil {
		d.body = &node{name: "body"}
	}
	return d, nil
}

// localName убирает namespace-префикс (например, "l:href" → "href").
func localName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	return strings.ToLower(s)
}

// attrsMap нормализует имена атрибутов (без префикса).
func attrsMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[localName(a.Name.Local)] = a.Value
	}
	return m
}

// children возвращает только дочерние элементы (без текста).
func (n *node) children() []*node {
	var out []*node
	for _, it := range n.content {
		if it.elem != nil {
			out = append(out, it.elem)
		}
	}
	return out
}

// plainText возвращает склеенный текст узла (для заголовков и т.п.).
func (n *node) plainText() string {
	var sb strings.Builder
	var walk func(*node)
	walk = func(x *node) {
		for _, it := range x.content {
			if it.elem != nil {
				walk(it.elem)
				continue
			}
			if s := strings.TrimSpace(it.text); s != "" {
				if sb.Len() > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(s)
			}
		}
	}
	walk(n)
	return sb.String()
}

// render — общий проход рендера.
func (d *doc) render(opt Options) (Result, error) {
	var sb strings.Builder
	var toc []TOCItem

	r := &renderer{sb: &sb, d: d, opt: opt, toc: &toc}
	for _, it := range d.body.content {
		if it.elem != nil {
			r.renderNode(it.elem, 0)
		} else if s := strings.TrimSpace(it.text); s != "" {
			r.sb.WriteString("<p>" + html.EscapeString(s) + "</p>")
		}
	}

	if opt.WithNotes && d.notes != nil {
		r.renderNotes(d.notes)
	}

	// Изображения: декодируем все <binary> (для выноса в файлы EPUB и т.п.).
	var images []Image
	for _, b := range d.binAll() {
		raw, err := base64.StdEncoding.DecodeString(b.data)
		if err != nil {
			continue
		}
		mt := b.mediaType
		if mt == "" {
			mt = "image/jpeg"
		}
		images = append(images, Image{ID: b.id, MediaType: mt, Data: raw})
	}

	return Result{HTML: sb.String(), TOC: toc, Images: images}, nil
}

// binAll возвращает бинари в стабильном порядке (по id), чтобы результат
// рендера был детерминированным.
func (d *doc) binAll() []*binary {
	ids := make([]string, 0, len(d.bin))
	for id := range d.bin {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*binary, 0, len(ids))
	for _, id := range ids {
		out = append(out, d.bin[id])
	}
	return out
}

// renderer — состояние одного прохода рендера.
type renderer struct {
	sb  *strings.Builder
	d   *doc
	opt Options
	toc *[]TOCItem
}

// renderChildren рендерит дочерние элементы узла.
func (r *renderer) renderChildren(n *node, level int) {
	for _, c := range n.children() {
		r.renderNode(c, level)
	}
}

// renderNode рендерит блочный элемент в HTML.
func (r *renderer) renderNode(n *node, level int) {
	switch n.name {
	case "section":
		title := sectionTitle(n)
		id := n.attrs["id"]
		if title != "" {
			if id == "" {
				id = fmt.Sprintf("sec-%d", len(*r.toc)+1)
			}
			*r.toc = append(*r.toc, TOCItem{ID: id, Title: title, Level: level})
			r.sb.WriteString(`<section class="fb2-section" id="` + escapeAttr(id) + `">`)
		} else {
			r.sb.WriteString(`<section class="fb2-section">`)
		}
		r.renderSectionBody(n, level)
		r.sb.WriteString("</section>")
	case "title":
		r.renderTitle(n, level+2)
	case "subtitle":
		r.sb.WriteString(`<p class="fb2-subtitle">`)
		r.writeInline(n)
		r.sb.WriteString("</p>")
	case "p":
		r.sb.WriteString("<p>")
		r.writeInline(n)
		r.sb.WriteString("</p>")
	case "empty-line":
		r.sb.WriteString(`<p class="fb2-empty"></p>`)
	case "image":
		r.renderImage(n)
	case "epigraph":
		r.sb.WriteString(`<blockquote class="fb2-epigraph">`)
		r.renderChildren(n, level)
		r.sb.WriteString("</blockquote>")
	case "cite":
		r.sb.WriteString(`<blockquote class="fb2-cite">`)
		r.renderChildren(n, level)
		r.sb.WriteString("</blockquote>")
	case "poem":
		r.renderPoem(n)
	case "stanza":
		r.renderStanza(n)
	case "text-author":
		r.sb.WriteString(`<p class="fb2-author">`)
		r.writeInline(n)
		r.sb.WriteString("</p>")
	case "date":
		r.sb.WriteString(`<p class="fb2-date">`)
		r.writeInline(n)
		r.sb.WriteString("</p>")
	case "table":
		r.renderTable(n)
	default:
		// Неизвестный контейнер: не теряем содержимое.
		if hasElems(n) {
			r.renderChildren(n, level)
		} else if s := strings.TrimSpace(n.plainText()); s != "" {
			r.sb.WriteString("<p>" + html.EscapeString(s) + "</p>")
		}
	}
}

// renderSectionBody рендерит содержимое секции, не дублируя её заголовок.
func (r *renderer) renderSectionBody(n *node, level int) {
	firstTitleSeen := false
	for _, it := range n.content {
		if it.elem == nil {
			continue
		}
		c := it.elem
		if !firstTitleSeen && c.name == "title" {
			r.renderTitle(c, level+2)
			firstTitleSeen = true
			continue
		}
		r.renderNode(c, level+1)
	}
}

// sectionTitle извлекает текст заголовка секции (первый <title>).
func sectionTitle(n *node) string {
	for _, c := range n.children() {
		if c.name == "title" {
			return strings.TrimSpace(c.plainText())
		}
	}
	return ""
}

// renderTitle рендерит <title> как заголовок нужного уровня.
func (r *renderer) renderTitle(n *node, level int) {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	tag := fmt.Sprintf("h%d", level)
	r.sb.WriteString("<" + tag + ` class="fb2-title">`)
	// Содержимое <title> — обычно один или несколько <p>: каждый на строке.
	first := true
	for _, c := range n.children() {
		if c.name == "p" || c.name == "subtitle" {
			if !first {
				r.sb.WriteString("<br/>")
			}
			r.writeInline(c)
			first = false
		}
	}
	if first {
		// Нет вложенных <p> — берём весь текст.
		if s := strings.TrimSpace(n.plainText()); s != "" {
			r.sb.WriteString(html.EscapeString(s))
		}
	}
	r.sb.WriteString("</" + tag + ">")
}

// renderPoem рендерит стихотворение: строфы + автор/дата.
func (r *renderer) renderPoem(n *node) {
	r.sb.WriteString(`<div class="fb2-poem">`)
	for _, c := range n.children() {
		if c.name == "title" {
			r.renderTitle(c, 3)
			continue
		}
		r.renderNode(c, 0)
	}
	r.sb.WriteString("</div>")
}

func (r *renderer) renderStanza(n *node) {
	r.sb.WriteString(`<div class="fb2-stanza">`)
	for _, c := range n.children() {
		switch c.name {
		case "v":
			r.sb.WriteString(`<p class="fb2-v">`)
			r.writeInline(c)
			r.sb.WriteString("</p>")
		case "title":
			r.renderTitle(c, 4)
		default:
			r.renderNode(c, 0)
		}
	}
	r.sb.WriteString("</div>")
}

// renderTable рендерит таблицу (tr/td/th).
func (r *renderer) renderTable(n *node) {
	r.sb.WriteString(`<table class="fb2-table">`)
	for _, row := range n.children() {
		if row.name != "tr" {
			continue
		}
		r.sb.WriteString("<tr>")
		for _, cell := range row.children() {
			if cell.name != "td" && cell.name != "th" {
				continue
			}
			tag := cell.name
			r.sb.WriteString("<" + tag + ">")
			if hasElems(cell) {
				r.writeInline(cell)
			} else {
				r.sb.WriteString(html.EscapeString(strings.TrimSpace(cell.plainText())))
			}
			r.sb.WriteString("</" + tag + ">")
		}
		r.sb.WriteString("</tr>")
	}
	r.sb.WriteString("</table>")
}

// renderImage рендерит <image l:href="#id">.
func (r *renderer) renderImage(n *node) {
	ref := strings.TrimSpace(n.attrs["href"])
	ref = strings.TrimPrefix(ref, "#")
	if ref == "" {
		return
	}
	src := ""
	if r.opt.ImageURL != nil {
		src = r.opt.ImageURL(ref)
	} else if b, ok := r.d.bin[ref]; ok {
		mt := b.mediaType
		if mt == "" {
			mt = "image/jpeg"
		}
		src = "data:" + mt + ";base64," + b.data
	}
	if src == "" {
		return
	}
	r.sb.WriteString(`<img class="fb2-image" src="` + escapeAttr(src) + `" alt="" loading="lazy"/>`)
}

// inlineTag возвращает имя HTML-тега для инлайн-элемента или "".
func inlineTag(name string) string {
	switch name {
	case "strong":
		return "strong"
	case "emphasis":
		return "em"
	case "sup":
		return "sup"
	case "sub":
		return "sub"
	case "code":
		return "code"
	case "strikethrough":
		return "s"
	case "style":
		return "span"
	default:
		return ""
	}
}

// writeInline записывает смешанный контент (текст и инлайн-теги) по порядку.
func (r *renderer) writeInline(n *node) {
	for _, it := range n.content {
		if it.elem == nil {
			r.sb.WriteString(formatText(it.text))
			continue
		}
		c := it.elem
		if tag := inlineTag(c.name); tag != "" {
			class := ""
			if c.name == "style" {
				class = ` class="fb2-style"`
			}
			r.sb.WriteString("<" + tag + class + ">")
			r.writeInline(c)
			r.sb.WriteString("</" + tag + ">")
			continue
		}
		switch c.name {
		case "a":
			r.renderLink(c)
		case "image":
			r.renderImage(c)
		default:
			// Блочный элемент внутри инлайна — рендерим как блок.
			r.renderNode(c, 0)
		}
	}
}

// renderLink рендерит <a>. Сноски (type="note") — как внутренние ссылки.
func (r *renderer) renderLink(n *node) {
	href := strings.TrimSpace(n.attrs["href"])
	kind := strings.ToLower(n.attrs["type"])
	if strings.HasPrefix(href, "#") {
		target := strings.TrimPrefix(href, "#")
		cls := "fb2-link"
		if kind == "note" {
			cls = "fb2-note-ref"
		}
		r.sb.WriteString(`<a class="` + cls + `" href="#` + escapeAttr(target) + `">`)
		r.writeInline(n)
		r.sb.WriteString("</a>")
		return
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		r.sb.WriteString(`<a class="fb2-link" href="` + escapeAttr(href) + `" rel="noopener noreferrer" target="_blank">`)
		r.writeInline(n)
		r.sb.WriteString("</a>")
		return
	}
	r.writeInline(n)
}

// renderNotes рендерит блок примечаний (<body name="notes">).
func (r *renderer) renderNotes(n *node) {
	type noteRef struct {
		id   string
		html string
	}
	var notes []noteRef
	for _, c := range n.children() {
		if c.name != "section" {
			continue
		}
		id := c.attrs["id"]
		var body strings.Builder
		sub := &renderer{sb: &body, d: r.d, opt: r.opt, toc: r.toc}
		for _, it := range c.content {
			if it.elem == nil {
				continue
			}
			// Пропускаем <title> (это номер примечания).
			if it.elem.name == "title" {
				continue
			}
			sub.renderNode(it.elem, 0)
		}
		notes = append(notes, noteRef{id: id, html: body.String()})
	}
	if len(notes) == 0 {
		return
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].id < notes[j].id })

	r.sb.WriteString(`<section class="fb2-notes"><h2 class="fb2-title">Примечания</h2>`)
	for _, nt := range notes {
		if nt.id == "" {
			continue
		}
		r.sb.WriteString(`<aside class="fb2-note" id="` + escapeAttr(nt.id) + `">`)
		r.sb.WriteString(nt.html)
		r.sb.WriteString(`</aside>`)
	}
	r.sb.WriteString(`</section>`)
}

// hasElems сообщает, есть ли у узла дочерние элементы.
func hasElems(n *node) bool {
	for _, it := range n.content {
		if it.elem != nil {
			return true
		}
	}
	return false
}

// formatText нормализует переводы строк и экранирует HTML.
func formatText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	return html.EscapeString(s)
}

// escapeAttr экранирует значение для атрибута.
func escapeAttr(s string) string {
	return html.EscapeString(s)
}

// DecodeBinary декодирует base64-содержимое картинки (для эндпоинта отдачи).
func DecodeBinary(mt, data string) (mediaType string, raw []byte, err error) {
	raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		return "", nil, err
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	return mt, raw, nil
}
