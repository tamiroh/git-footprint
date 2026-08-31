// Package image reads EXIF and XMP metadata from committed image blobs.
package image

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/evanoberholster/imagemeta"

	"github.com/tamiroh/git-footprint/internal/rule"
	"github.com/tamiroh/git-footprint/internal/xmp"
)

var exts = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true, ".png": true,
	".tif": true, ".tiff": true, ".dng": true, ".cr2": true, ".crw": true,
	".arw": true, ".nef": true, ".gif": true,
}

// claimed but never read: nothing in an icon can identify anyone.
var inert = map[string]bool{".ico": true, ".icns": true}

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }

type data struct {
	gps, creator, owner, serial, camera, software, taken string
	people                                               []string
}

func (d data) empty() bool {
	return d.gps == "" && d.creator == "" && d.owner == "" && d.serial == "" &&
		d.camera == "" && d.software == "" && d.taken == "" && len(d.people) == 0
}

type item struct {
	data
	path, link string
	by         rule.Author
}

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
		checks := []rule.Check{
			{Name: "image-location", Level: rule.Warn, Value: it.gps},
			{Name: "image-creator", Level: rule.Warn, Value: it.creator},
			{Name: "image-owner", Level: rule.Warn, Value: it.owner},
		}
		for _, p := range it.people {
			checks = append(checks, rule.Check{Name: "image-person", Level: rule.Warn, Value: p})
		}
		checks = append(checks,
			rule.Check{Name: "image-camera", Level: rule.Info, Value: it.camera},
			rule.Check{Name: "image-serial", Level: rule.Info, Value: it.serial},
			rule.Check{Name: "image-software", Level: rule.Info, Value: it.software},
			rule.Check{Name: "image-date", Level: rule.Info, Value: it.taken},
		)
		out = append(out, rule.Finding{
			Detector: "image-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty(checks),
		})
	}
	return out
}

func revealing(d data) bool {
	return d.gps != "" || d.creator != "" || d.owner != "" || len(d.people) > 0
}

func dedupe(in []item) []item {
	type key struct{ gps, creator, owner, serial, camera, software, taken, people, path, name, email string }
	seen := map[key]bool{}
	var out []item
	for _, it := range in {
		k := key{
			it.gps, it.creator, it.owner, it.serial, it.camera, it.software, it.taken,
			strings.Join(it.people, "\x00"), it.path, it.by.Name, it.by.Email,
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}

func read(blob []byte) (d data) {
	defer func() { _ = recover() }()

	if !bytes.HasPrefix(blob, []byte("GIF8")) {
		d = readEXIF(blob)
	}

	// XMP fills what EXIF can't: face-tagged names, and any blank creator /
	// software / date (for a GIF that's everything).
	if x := xmp.Read(blob); !x.Empty() {
		d.people = x.People
		if d.creator == "" {
			d.creator = x.Creator
		}
		if d.software == "" {
			d.software = x.Tool
		}
		if d.taken == "" {
			d.taken = x.Date
		}
	}
	return
}

func readEXIF(blob []byte) (d data) {
	// imagemeta's PNG scanner misreads little-endian EXIF; pull the eXIf chunk
	// ourselves and feed the raw TIFF to the endian-aware path.
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
	d.creator = clean(x.IFD0.Artist)
	if d.creator == "" {
		d.creator = clean(x.IFD0.Copyright)
	}
	d.owner = clean(x.ExifIFD.CameraOwnerName)
	d.serial = firstNonEmpty(x.ExifIFD.BodySerialNumber, x.CameraSerial, x.ExifIFD.LensSerial)
	d.camera = cameraName(clean(x.CameraMake()), clean(x.IFD0.Model))
	d.software = clean(x.IFD0.Software)
	if t := x.OriginalDate(); plausible(t) {
		d.taken = t.Format("2006-01-02 15:04:05")
	}
	return
}

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
		p += n + 4
	}
	return nil
}

func clean(s string) string { return strings.TrimRight(s, "\x00 ") }

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func plausible(t time.Time) bool { return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1 }

// cameraName: "NIKON" + "NIKON D2H" -> "NIKON D2H"
func cameraName(mk, model string) string {
	switch {
	case mk == "":
		return model
	case model == "":
		return mk
	}
	if brand := strings.Fields(mk); len(brand) > 0 &&
		strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand[0])) {
		return model
	}
	return mk + " " + model
}
