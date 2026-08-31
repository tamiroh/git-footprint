// Package xmp reads the XMP metadata packet an image, PDF or video may carry.
package xmp

import (
	"bytes"
	"encoding/xml"
	"time"

	"github.com/tamiroh/git-footprint/internal/mediameta"
)

type Data struct {
	Creator string // dc:creator
	Tool    string // xmp:CreatorTool
	Date    string // xmp:CreateDate / photoshop:DateCreated, normalised
}

func (d Data) Empty() bool { return d == Data{} }

// Read parses the first <x:xmpmeta> … </x:xmpmeta> packet in blob.
func Read(blob []byte) Data {
	if p := packetOf(blob); p != nil {
		return parse(p)
	}
	return Data{}
}

// All parses every packet — a PDF keeps one per incremental save, so an old
// author survives here after being cleared from the Info dictionary.
func All(blob []byte) []Data {
	var out []Data
	for rest := blob; ; {
		p := packetOf(rest)
		if p == nil {
			return out
		}
		if d := parse(p); !d.Empty() {
			out = append(out, d)
		}
		rest = rest[bytes.Index(rest, p)+len(p):]
	}
}

func parse(packet []byte) (d Data) {
	var m meta
	if xml.Unmarshal(packet, &m) != nil {
		return
	}
	for _, desc := range m.Desc {
		if d.Creator == "" {
			d.Creator = mediameta.FirstNonEmpty(desc.Creators...)
		}
		if d.Tool == "" {
			d.Tool = mediameta.Clean(mediameta.FirstNonEmpty(desc.ToolAttr, desc.ToolEl))
		}
		if d.Date == "" {
			d.Date = normDate(mediameta.FirstNonEmpty(desc.DateAttr, desc.DateEl, desc.DateCreated))
		}
	}
	return
}

func packetOf(b []byte) []byte {
	i := bytes.Index(b, []byte("<x:xmpmeta"))
	if i < 0 {
		return nil
	}
	end := []byte("</x:xmpmeta>")
	j := bytes.Index(b[i:], end)
	if j < 0 {
		return nil
	}
	return b[i : i+j+len(end)]
}

type meta struct {
	Desc []desc `xml:"RDF>Description"`
}

// RDF carries a property as an attribute or a child element; XMP uses both.
type desc struct {
	ToolAttr    string   `xml:"CreatorTool,attr"`
	ToolEl      string   `xml:"CreatorTool"`
	DateAttr    string   `xml:"CreateDate,attr"`
	DateEl      string   `xml:"CreateDate"`
	DateCreated string   `xml:"DateCreated"`
	Creators    []string `xml:"creator>Seq>li"`
}

func normDate(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil && mediameta.Plausible(t) {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}
