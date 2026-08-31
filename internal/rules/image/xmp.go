package image

import (
	"bytes"
	"encoding/xml"
	"strings"
	"time"

	"github.com/tamiroh/git-footprint/internal/mediameta"
)

// readXMP pulls creator, authoring tool and creation date from an XMP packet,
// the only metadata channel a GIF has.
func readXMP(blob []byte) (d data) {
	packet := xmpPacket(blob)
	if packet == nil {
		return
	}
	var m xmpMeta
	if xml.Unmarshal(packet, &m) != nil {
		return
	}
	for _, x := range m.Desc {
		if d.creator == "" {
			d.creator = firstNonEmpty(x.Creators...)
		}
		if d.software == "" {
			d.software = mediameta.Clean(firstNonEmpty(x.ToolAttr, x.ToolEl))
		}
		if d.taken == "" {
			d.taken = xmpDate(firstNonEmpty(x.DateAttr, x.DateEl, x.DateCreated))
		}
	}
	return
}

func xmpPacket(b []byte) []byte {
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

type xmpMeta struct {
	Desc []xmpDesc `xml:"RDF>Description"`
}

// RDF carries a property as an attribute or a child element; XMP uses both.
type xmpDesc struct {
	ToolAttr    string   `xml:"CreatorTool,attr"`
	ToolEl      string   `xml:"CreatorTool"`
	DateAttr    string   `xml:"CreateDate,attr"`
	DateEl      string   `xml:"CreateDate"`
	DateCreated string   `xml:"DateCreated"`
	Creators    []string `xml:"creator>Seq>li"`
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func xmpDate(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil && mediameta.Plausible(t) {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}
