package opds

import (
	"encoding/json"
	"io"
)

// OPDS 2.0 (JSON) структуры (Readium Web Publication Manifest, упрощённо).

type jsonFeed struct {
	Metadata jsonFeedMeta `json:"metadata"`
	Links    []jsonLink   `json:"links"`
	Nav      []jsonLink   `json:"navigation,omitempty"`
	Pub      []jsonPub    `json:"publications,omitempty"`
}

type jsonFeedMeta struct {
	Title         string `json:"title"`
	Modified      string `json:"modified"`
	Type          string `json:"@type,omitempty"`
	NumberOfItems int    `json:"numberOfItems,omitempty"`
	Author        string `json:"author,omitempty"`
}

type jsonLink struct {
	Rel   string `json:"rel,omitempty"`
	Href  string `json:"href"`
	Type  string `json:"type,omitempty"`
	Title string `json:"title,omitempty"`
}

type jsonPub struct {
	Metadata jsonPubMeta `json:"metadata"`
	Links    []jsonLink  `json:"links"`
}

type jsonPubMeta struct {
	Type        string     `json:"@type"`
	Title       string     `json:"title"`
	Identifier  string     `json:"identifier"`
	Modified    string     `json:"modified"`
	Description string     `json:"description,omitempty"`
	Author      []string   `json:"author,omitempty"`
	Subject     []jsonSubj `json:"subject,omitempty"`
}

type jsonSubj struct {
	Name   string `json:"name"`
	Scheme string `json:"scheme,omitempty"`
	Code   string `json:"code,omitempty"`
}

// JSON записывает feed в формате OPDS 2.0 (JSON).
func JSON(w io.Writer, f Feed) error {
	jf := jsonFeed{
		Metadata: jsonFeedMeta{
			Title:    f.Title,
			Modified: f.Updated,
			Type:     "Feed",
			Author:   "Easy OPDS",
		},
	}
	if f.Self.Href != "" {
		jf.Links = append(jf.Links, jsonLink{Rel: RelSelf, Href: f.Self.Href, Type: TypeOPDS2})
	}
	if f.Start != "" {
		jf.Links = append(jf.Links, jsonLink{Rel: RelStart, Href: f.Start, Type: TypeOPDS2})
	}
	if f.Next != "" {
		jf.Links = append(jf.Links, jsonLink{Rel: RelNext, Href: f.Next, Type: TypeOPDS2})
	}
	if f.Prev != "" {
		jf.Links = append(jf.Links, jsonLink{Rel: RelPrevious, Href: f.Prev, Type: TypeOPDS2})
	}
	if f.Search != "" {
		jf.Links = append(jf.Links, jsonLink{Rel: RelSearch, Href: f.Search, Type: TypeSearch})
	}

	for _, e := range f.Entries {
		// Навигационные записи в 2.0 — это navigation links.
		if f.Kind == "navigation" {
			navAdded := false
			for _, l := range e.Links {
				if l.Rel == RelSubsection {
					jf.Nav = append(jf.Nav, jsonLink{Rel: RelSubsection, Href: l.Href, Type: TypeOPDS2, Title: e.Title})
					navAdded = true
				}
			}
			if navAdded {
				continue
			}
		}

		pub := jsonPub{Metadata: jsonPubMeta{
			Type:        "http://schema.org/Book",
			Title:       e.Title,
			Identifier:  e.ID,
			Modified:    e.Updated,
			Description: e.Summary,
		}}
		for _, a := range e.Authors {
			pub.Metadata.Author = append(pub.Metadata.Author, a.Name)
		}
		for _, c := range e.Categories {
			pub.Metadata.Subject = append(pub.Metadata.Subject, jsonSubj{Name: c.Label, Code: c.Term})
		}
		for _, l := range e.Links {
			if l.Rel == RelSubsection {
				continue
			}
			pub.Links = append(pub.Links, jsonLink{Rel: l.Rel, Href: l.Href, Type: l.Type, Title: l.Title})
		}
		jf.Pub = append(jf.Pub, pub)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(jf)
}
