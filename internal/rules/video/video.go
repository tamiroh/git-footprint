// Package video is the rule that reads QuickTime / ISOBMFF container metadata —
// location, creator, recording device, creation date — from committed MP4 and
// MOV blobs.
package video

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abema/go-mp4"

	"github.com/tamiroh/git-footprint/internal/mediameta"
	"github.com/tamiroh/git-footprint/internal/rule"
)

var exts = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".qt": true}

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }

type data struct{ gps, creator, camera, taken string }

func (d data) empty() bool { return d == data{} }

type item struct {
	data
	path, link string
	by         rule.Author
}

// Rule accumulates one item per video blob that carried metadata.
type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return exts[ext(name)] }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	if !exts[ext(b.Name)] {
		return
	}
	ctx.Claim()
	if d := read(b.Content); !d.empty() {
		r.items = append(r.items, item{d, b.Path, ctx.Link(b, true), b.By})
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
			Detector: "video-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty([]rule.Check{
				{Name: "video-location", Level: rule.Warn, Value: it.gps},
				{Name: "video-creator", Level: rule.Warn, Value: it.creator},
				{Name: "video-camera", Level: rule.Info, Value: it.camera},
				{Name: "video-date", Level: rule.Info, Value: it.taken},
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

// read pulls QuickTime / ISOBMFF metadata from an MP4 or MOV blob.
func read(blob []byte) (d data) {
	defer func() { _ = recover() }()
	r := bytes.NewReader(blob)

	var mk, model string
	var keys []string // QuickTime metadata-item keys, in index order

	apply := func(key, val string) {
		switch {
		case strings.HasSuffix(key, "location.ISO6709") || key == "\xa9xyz":
			if lat, lon, ok := parseISO6709(val); ok {
				d.gps = fmt.Sprintf("%.5f, %.5f", lat, lon)
			}
		case strings.HasSuffix(key, "creationdate") || key == "\xa9day":
			if tm := normalizeTime(val); tm != "" {
				d.taken = tm // more specific than an mvhd mux date; overrides it
			}
		case strings.HasSuffix(key, ".make") || key == "\xa9mak":
			if mk == "" {
				mk = val
			}
		case strings.HasSuffix(key, ".model") || key == "\xa9mod":
			if model == "" {
				model = val
			}
		case strings.HasSuffix(key, ".artist") || key == "\xa9ART" || key == "\xa9aut":
			if d.creator == "" {
				d.creator = mediameta.Clean(val)
			}
		}
	}

	_, _ = mp4.ReadBoxStructure(r, func(h *mp4.ReadHandle) (any, error) {
		t := h.BoxInfo.Type.String()
		raw := string(h.BoxInfo.Type[:]) // "©" atoms: raw is "\xa9xyz", t is "(c)xyz"
		switch {
		case len(h.Path) > 12:
			return nil, nil // a real box tree is ~7 deep; deeper is a crafted bomb

		case t == "keys":
			if pl, _, err := h.ReadPayload(); err == nil {
				if k, ok := pl.(*mp4.Keys); ok {
					for _, e := range k.Entries {
						keys = append(keys, string(e.KeyValue))
					}
				}
			}

		case underIlst(h.Path): // an ilst child
			pl, _, err := h.ReadPayload()
			if err != nil {
				return nil, nil
			}
			// Apple's keys-indexed items (0x00000001…) absorb their data box;
			// the four-char atoms (©ART…) keep it as a separate child to expand.
			if it, ok := pl.(*mp4.Item); ok {
				if i := int(binary.BigEndian.Uint32(h.BoxInfo.Type[:])); i >= 1 && i <= len(keys) {
					apply(keys[i-1], string(it.Data.Data))
				}
				return nil, nil
			}
			return h.Expand()

		case t == "data": // the value box under a four-char ilst atom
			if pl, _, err := h.ReadPayload(); err == nil {
				if val, ok := pl.(*mp4.Data); ok {
					apply(ilstKey(h.Path, keys), string(val.Data))
				}
			}

		case containerBox[t]:
			return h.Expand()

		case raw == "\xa9xyz": // Android / QuickTime ISO6709 location box
			if b := readRaw(h); len(b) > 4 {
				if lat, lon, ok := parseISO6709(string(b[4:])); ok {
					d.gps = fmt.Sprintf("%.5f, %.5f", lat, lon)
				}
			}

		case raw == "loci": // 3GPP / ffmpeg location box
			if lat, lon, ok := parseLoci(readRaw(h)); ok {
				d.gps = fmt.Sprintf("%.5f, %.5f", lat, lon)
			}

		case t == "mvhd":
			// the movie header's creation time: a real capture date on
			// camera-original and screen-recorded files, the mux time on
			// re-encoded ones. Only a fallback — an ilst creationdate wins.
			if d.taken == "" {
				if pl, _, err := h.ReadPayload(); err == nil {
					if m, ok := pl.(*mp4.Mvhd); ok {
						if u := time.Unix(mvhdSeconds(m)-2082844800, 0).UTC(); mediameta.Plausible(u) {
							d.taken = u.Format("2006-01-02 15:04:05")
						}
					}
				}
			}
		}
		return nil, nil
	})

	d.camera = mediameta.CameraName(mediameta.Clean(mk), mediameta.Clean(model))
	return
}

var containerBox = map[string]bool{
	"moov": true, "udta": true, "meta": true, "ilst": true,
}

func underIlst(path mp4.BoxPath) bool {
	return len(path) >= 2 && string(path[len(path)-2][:]) == "ilst"
}

func mvhdSeconds(m *mp4.Mvhd) int64 {
	if m.CreationTimeV1 != 0 {
		return int64(m.CreationTimeV1)
	}
	return int64(m.CreationTimeV0)
}

// ilstKey resolves the key name for a "data" box: its parent ilst entry is
// either a numeric 1-based index into keys, or a four-char atom like "\xa9ART".
func ilstKey(path mp4.BoxPath, keys []string) string {
	if len(path) < 2 {
		return ""
	}
	parent := path[len(path)-2]
	if idx := binary.BigEndian.Uint32(parent[:]); int(idx) >= 1 && int(idx) <= len(keys) {
		return keys[idx-1]
	}
	return string(parent[:])
}

func readRaw(h *mp4.ReadHandle) []byte {
	var buf bytes.Buffer
	if _, err := h.ReadData(&buf); err != nil {
		return nil
	}
	return buf.Bytes()
}

// parseISO6709 parses "+37.4219-122.0840+010.000/" style coordinates.
func parseISO6709(s string) (lat, lon float64, ok bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "/"))
	if len(s) < 2 {
		return 0, 0, false
	}
	i := strings.IndexAny(s[1:], "+-")
	if i < 0 {
		return 0, 0, false
	}
	i++
	lat, e1 := strconv.ParseFloat(s[:i], 64)
	rest := s[i:]
	if j := strings.IndexAny(rest[1:], "+-"); j >= 0 {
		rest = rest[:j+1]
	}
	lon, e2 := strconv.ParseFloat(rest, 64)
	if e1 != nil || e2 != nil || (lat == 0 && lon == 0) {
		return 0, 0, false
	}
	return lat, lon, true
}

// parseLoci reads a 3GPP "loci" location box body: version+flags (4), language
// (2), null-terminated name, role (1), then 16.16 fixed-point longitude,
// latitude, altitude.
func parseLoci(b []byte) (lat, lon float64, ok bool) {
	if len(b) < 6 {
		return 0, 0, false
	}
	p := 6
	for p < len(b) && b[p] != 0 {
		p++
	}
	p++ // null terminator
	if p+13 > len(b) {
		return 0, 0, false
	}
	p++ // role
	lon = fixed1616(b[p:])
	lat = fixed1616(b[p+4:])
	return lat, lon, lat != 0 || lon != 0
}

func fixed1616(b []byte) float64 {
	return float64(int32(binary.BigEndian.Uint32(b))) / 65536
}

func normalizeTime(s string) string {
	for _, layout := range []string{
		time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05", "2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil && mediameta.Plausible(t) {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}
