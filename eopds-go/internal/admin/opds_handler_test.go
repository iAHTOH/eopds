package admin

import (
	"net/http/httptest"
	"testing"
)

func TestOPDSWantJSON(t *testing.T) {
	cases := []struct {
		path   string
		query  string
		accept string
		want   bool
	}{
		{"/opds", "", "", false},                                            // по умолчанию Atom
		{"/opds", "?format=json", "", true},                                 // явный json
		{"/opds", "?format=atom", "", false},                                // явный atom
		{"/opds", "", "application/opds+json", true},                        // Accept opds+json
		{"/opds", "", "application/atom+xml", false},                        // Accept atom
		{"/opds", "", "application/opds+json, application/atom+xml", false}, // оба → Atom
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.path+c.query, nil)
		if c.accept != "" {
			r.Header.Set("Accept", c.accept)
		}
		if got := opdsWantJSON(r); got != c.want {
			t.Errorf("opdsWantJSON(%q,%q,Accept=%q) = %v, want %v",
				c.path, c.query, c.accept, got, c.want)
		}
	}
}

func TestMimeForFormat(t *testing.T) {
	cases := map[string]string{
		"fb2":  "application/x-fictionbook+xml",
		"epub": "application/epub+zip",
		"pdf":  "application/pdf",
		"txt":  "text/plain",
		"xyz":  "application/octet-stream",
	}
	for in, want := range cases {
		if got := mimeForFormat(in); got != want {
			t.Errorf("mimeForFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
