package fb2

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtractCover(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
 <description><title-info><book-title>X</book-title>
  <coverpage><image l:href="#cover.jpg"/></coverpage>
 </title-info></description>
 <body><section><p>t</p></section></body>
 <binary id="cover.jpg" content-type="image/jpeg">/9j/4AAQSkZJRg==</binary>
</FictionBook>`
	c, err := ExtractCover(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}
	if len(c.Data) == 0 || !bytes.HasPrefix(c.Data, []byte{0xFF, 0xD8, 0xFF}) {
		t.Fatalf("bad jpeg data: %v", c.Data)
	}
	if c.ContentType != "image/jpeg" || c.Ext() != "jpg" {
		t.Fatalf("content-type/ext = %s/%s", c.ContentType, c.Ext())
	}
}

func TestExtractCoverAbsent(t *testing.T) {
	const doc = `<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info><book-title>X</book-title></title-info></description></FictionBook>`
	if _, err := ExtractCover(strings.NewReader(doc)); err != ErrNoCover {
		t.Fatalf("expected ErrNoCover, got %v", err)
	}
}
