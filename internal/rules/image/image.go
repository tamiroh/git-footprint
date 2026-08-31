// Package image is the rule that reads EXIF metadata — location, artist, camera,
// authoring software, capture date — from committed image blobs.
package image

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanoberholster/imagemeta"

	"github.com/tamiroh/git-footprint/internal/mediameta"
	"github.com/tamiroh/git-footprint/internal/rule"
)

// HEIC/HEIF/AVIF/CR3 go through imagemeta v1.0.0's ISOBMFF path, which currently
// returns nothing — better to leave them in the "not read" tally than to
// silently pass an iPhone HEIC that carries GPS.
var exts = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true, ".png": true,
	".tif": true, ".tiff": true, ".dng": true, ".cr2": true, ".crw": true,
	".arw": true, ".nef": true,
}

// inert image formats carry no field that can hold identifying metadata, so the
// scan claims them silently rather than listing them as unread.
var inert = map[string]bool{".ico": true, ".icns": true}

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }

type data struct{ gps, creator, camera, software, taken string }

func (d data) empty() bool { return d == data{} }

type item struct {
	data
	path, link string
	by         rule.Author
}

// Rule accumulates one item per image blob that carried metadata.
type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return exts[ext(name)] }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	switch e := ext(b.Name); {
	case inert[e]:
		ctx.Claim()
	case exts[e]:
		ctx.Claim()
		if d := read(b.Content); !d.empty() {
			r.items = append(r.items, item{d, b.Path, ctx.Link(b, true), b.By})
		}
	}
}

func (r *Rule) Findings() []rule.Finding {
	r.items = dedupe(r.items)
	sort.SliceStable(r.items, func(i, j int) bool {
		if a, b := revealing(r.items[i].data), revealing(r.items[j].data); a != b {
			return a
		}
		return r.items[i].path < r.items[j].path
	})

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, rule.Finding{
			Detector: "image-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty([]rule.Check{
				{Name: "image-location", Level: rule.Warn, Value: it.gps},
				{Name: "image-creator", Level: rule.Warn, Value: it.creator},
				{Name: "image-camera", Level: rule.Info, Value: it.camera},
				{Name: "image-software", Level: rule.Info, Value: it.software},
				{Name: "image-date", Level: rule.Info, Value: it.taken},
			}),
		})
	}
	return out
}

func revealing(d data) bool { return d.gps != "" || d.creator != "" }

func dedupe(in []item) []item {
	type key struct {
		data
		path, name, email string
	}
	seen := map[key]bool{}
	var out []item
	for _, it := range in {
		k := key{it.data, it.path, it.by.Name, it.by.Email}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}

func read(blob []byte) (d data) {
	defer func() { _ = recover() }() // parsers can panic on hostile input

	// imagemeta's PNG scanner assumes big-endian EXIF; pull the eXIf chunk
	// ourselves and hand the raw TIFF stream to the (endian-aware) TIFF path.
	decode, src := imagemeta.Decode, bytes.NewReader(blob)
	if payload := pngEXIF(blob); payload != nil {
		decode, src = imagemeta.DecodeTiff, bytes.NewReader(payload)
	}
	x, err := decode(src)
	if err != nil {
		return
	}

	if lat, long := x.GPS.Latitude(), x.GPS.Longitude(); lat != 0 || long != 0 {
		d.gps = fmt.Sprintf("%.5f, %.5f", lat, long)
	}
	d.creator = mediameta.Clean(x.IFD0.Artist)
	if d.creator == "" {
		d.creator = mediameta.Clean(x.IFD0.Copyright)
	}
	d.camera = mediameta.CameraName(mediameta.Clean(x.CameraMake()), mediameta.Clean(x.IFD0.Model))
	d.software = mediameta.Clean(x.IFD0.Software) // OS version on phones, editor name on desktop
	if t := x.OriginalDate(); mediameta.Plausible(t) {
		d.taken = t.Format("2006-01-02 15:04:05")
	}
	return
}

// pngEXIF returns the raw EXIF (TIFF) payload from a PNG's eXIf chunk, or nil.
func pngEXIF(b []byte) []byte {
	if len(b) < 8 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil
	}
	for p := 8; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		p += 8
		if n < 0 || p+n > len(b) {
			return nil
		}
		switch typ {
		case "eXIf":
			return b[p : p+n]
		case "IEND":
			return nil
		}
		p += n + 4 // chunk data + CRC
	}
	return nil
}
