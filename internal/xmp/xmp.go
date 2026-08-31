// Package xmp reads the XMP metadata packet an image, PDF or video may carry.
package xmp

import (
	"bytes"
	"encoding/xml"
	"strings"
	"time"
)

type Data struct {
	Creator string   // dc:creator
	Tool    string   // xmp:CreatorTool
	Date    string   // xmp:CreateDate / photoshop:DateCreated, normalised
	People  []string // names tagged on face regions (mwg-rs / Microsoft)
}

func (d Data) Empty() bool {
	return d.Creator == "" && d.Tool == "" && d.Date == "" && len(d.People) == 0
}

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
	seen := map[string]bool{}
	for _, desc := range m.Desc {
		if d.Creator == "" {
			d.Creator = firstNonEmpty(desc.Creators...)
		}
		if d.Tool == "" {
			d.Tool = firstNonEmpty(desc.ToolAttr, desc.ToolEl)
		}
		if d.Date == "" {
			d.Date = normDate(firstNonEmpty(desc.DateAttr, desc.DateEl, desc.DateCreated))
		}
		for _, list := range [][]string{desc.RegionNames, desc.MSRegionNames} {
			for _, name := range list {
				if n := strings.TrimSpace(name); n != "" && !seen[n] {
					seen[n] = true
					d.People = append(d.People, n)
				}
			}
		}
	}
	return
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
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
	ToolAttr      string   `xml:"CreatorTool,attr"`
	ToolEl        string   `xml:"CreatorTool"`
	DateAttr      string   `xml:"CreateDate,attr"`
	DateEl        string   `xml:"CreateDate"`
	DateCreated   string   `xml:"DateCreated"`
	Creators      []string `xml:"creator>Seq>li"`
	RegionNames   []string `xml:"Regions>RegionList>Bag>li>Name"`              // mwg-rs
	MSRegionNames []string `xml:"RegionInfo>Regions>Bag>li>PersonDisplayName"` // Microsoft
}

func normDate(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil && plausible(t) {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

// plausible rejects dates a corrupt metadata field would otherwise render literally.
func plausible(t time.Time) bool { return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1 }
