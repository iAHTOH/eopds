package opds

import (
	"encoding/xml"
	"io"
)

// Atom 1.2 (OPDS 1.2) структуры для encoding/xml.

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	XMLNS   string      `xml:"xmlns,attr"`
	XMLNSDC string      `xml:"xmlns:dc,attr"`
	XMLNSOS string      `xml:"xmlns:opensearch,attr"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Icon    string      `xml:"icon,omitempty"`
	Author  *atomAuthor `xml:"author"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string       `xml:"title"`
	ID         string       `xml:"id"`
	Updated    string       `xml:"updated"`
	Authors    []atomAuthor `xml:"author"`
	Summary    string       `xml:"summary,omitempty"`
	Links      []atomLink   `xml:"link"`
	Categories []atomCat    `xml:"category,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type atomLink struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

type atomCat struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

// Atom записывает feed в формате OPDS 1.2 (Atom XML).
func Atom(w io.Writer, f Feed) error {
	af := atomFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		XMLNSDC: "http://purl.org/dc/terms/",
		XMLNSOS: "http://a9.com/-/spec/opensearch/1.1/",
		ID:      f.ID,
		Title:   f.Title,
		Updated: f.Updated,
		Author:  &atomAuthor{Name: "Easy OPDS", URI: f.Start},
	}
	// Ссылки каталога: self (+ next/prev/start/search).
	if f.Self.Href != "" {
		af.Links = append(af.Links, atomLink{Rel: f.Self.Rel, Href: f.Self.Href, Type: f.Self.Type})
	}
	if f.Start != "" {
		af.Links = append(af.Links, atomLink{Rel: RelStart, Href: f.Start, Type: TypeNavigation})
	}
	if f.Next != "" {
		af.Links = append(af.Links, atomLink{Rel: RelNext, Href: f.Next, Type: f.Self.Type})
	}
	if f.Prev != "" {
		af.Links = append(af.Links, atomLink{Rel: RelPrevious, Href: f.Prev, Type: f.Self.Type})
	}
	if f.Search != "" {
		af.Links = append(af.Links, atomLink{Rel: RelSearch, Href: f.Search, Type: TypeSearch})
	}

	for _, e := range f.Entries {
		ae := atomEntry{
			Title:   e.Title,
			ID:      e.ID,
			Updated: e.Updated,
			Summary: e.Summary,
		}
		for _, a := range e.Authors {
			ae.Authors = append(ae.Authors, atomAuthor{Name: a.Name, URI: a.URI})
		}
		for _, l := range e.Links {
			ae.Links = append(ae.Links, atomLink{Rel: l.Rel, Href: l.Href, Type: l.Type, Title: l.Title})
		}
		for _, c := range e.Categories {
			ae.Categories = append(ae.Categories, atomCat{Term: c.Term, Label: c.Label})
		}
		af.Entries = append(af.Entries, ae)
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(af); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
