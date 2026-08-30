// This file wraps imagemeta, go-mp4 and rsc.io/pdf; the exported metadata rule
// lives in metadata.go.
package metadata

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abema/go-mp4"
	"github.com/evanoberholster/imagemeta"
	"rsc.io/pdf"
)

var (
	imageExts = set(".jpg", ".jpeg", ".jpe", ".jfif", ".png", ".tif", ".tiff",
		".dng", ".heic", ".heif", ".avif", ".cr2", ".cr3", ".crw", ".arw", ".nef")
	videoExts = set(".mp4", ".m4v", ".mov", ".qt")
	docExts   = set(".pdf")
	// inertExts are binary formats with no field that can carry identifying
	// metadata, so scanning skips them silently rather than listing them as
	// unread. Kept deliberately small: fonts (name table) and compiled objects
	// (embedded source paths) do not qualify.
	inertExts = set(".ico", ".icns")
)

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func ext(path string) string { return strings.ToLower(filepath.Ext(path)) }

func isImage(path string) bool { return imageExts[ext(path)] }
func isVideo(path string) bool { return videoExts[ext(path)] }
func isDoc(path string) bool   { return docExts[ext(path)] }
func handles(path string) bool { return isImage(path) || isVideo(path) || isDoc(path) }
func inert(path string) bool   { return inertExts[ext(path)] }

type Data struct {
	GPS      string // "lat, long"
	Creator  string // Artist / author / copyright
	Camera   string // "Make Model"
	Software string // authoring application or OS
	Taken    string // "2006-01-02 15:04:05"
}

func (d Data) Empty() bool {
	return d.GPS == "" && d.Creator == "" && d.Camera == "" && d.Software == "" && d.Taken == ""
}

func (d Data) Revealing() bool { return d.GPS != "" || d.Creator != "" }

// read pulls metadata from a committed blob, dispatching on the path's extension.
func read(path string, blob []byte) Data {
	switch {
	case isVideo(path):
		return readVideo(blob)
	case isImage(path):
		return readImage(blob)
	case isDoc(path):
		return readPDF(blob)
	default:
		return Data{}
	}
}

func readImage(blob []byte) (d Data) {
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
		d.GPS = fmt.Sprintf("%.5f, %.5f", lat, long)
	}
	d.Creator = clean(x.IFD0.Artist)
	if d.Creator == "" {
		d.Creator = clean(x.IFD0.Copyright)
	}
	d.Camera = cameraName(clean(x.CameraMake()), clean(x.IFD0.Model))
	d.Software = clean(x.IFD0.Software) // OS version on phones, editor name on desktop
	if t := x.OriginalDate(); plausible(t) {
		d.Taken = t.Format("2006-01-02 15:04:05")
	}
	return
}

func clean(s string) string { return strings.TrimRight(s, "\x00 ") }

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

func cameraName(mk, model string) string {
	switch {
	case mk == "":
		return model
	case model == "":
		return mk
	}
	if brand := strings.Fields(mk); len(brand) > 0 &&
		strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand[0])) {
		return model // model already carries the maker, e.g. "NIKON D2H"
	}
	return mk + " " + model
}

// --- video ---------------------------------------------------------------

// readVideo pulls QuickTime / ISOBMFF metadata from an MP4 or MOV blob.
func readVideo(blob []byte) (d Data) {
	defer func() { _ = recover() }()
	r := bytes.NewReader(blob)

	var mk, model string
	var keys []string // QuickTime metadata-item keys, in index order

	_, _ = mp4.ReadBoxStructure(r, func(h *mp4.ReadHandle) (any, error) {
		t := h.BoxInfo.Type.String()
		raw := string(h.BoxInfo.Type[:]) // "©" atoms: raw is "\xa9xyz", t is "(c)xyz"
		switch {
		case len(h.Path) > 12:
			return nil, nil // a real box tree is ~7 deep; deeper is a crafted bomb
		case containerBox[t] || underIlst(h.Path):
			return h.Expand()

		case t == "keys":
			if pl, _, err := h.ReadPayload(); err == nil {
				if k, ok := pl.(*mp4.Keys); ok {
					for _, e := range k.Entries {
						keys = append(keys, string(e.KeyValue))
					}
				}
			}

		case t == "data": // an ilst value; its parent names it
			pl, _, err := h.ReadPayload()
			if err != nil {
				return nil, nil
			}
			data, ok := pl.(*mp4.Data)
			if !ok {
				return nil, nil
			}
			key := ilstKey(h.Path, keys)
			val := string(data.Data)
			switch {
			case strings.HasSuffix(key, "location.ISO6709") || key == "\xa9xyz":
				if lat, lon, ok := parseISO6709(val); ok {
					d.GPS = fmt.Sprintf("%.5f, %.5f", lat, lon)
				}
			case strings.HasSuffix(key, "creationdate") || key == "\xa9day":
				if t := normalizeTime(val); t != "" {
					d.Taken = t // keep an earlier mvhd date if this one is unparseable
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
				if d.Creator == "" {
					d.Creator = clean(val)
				}
			}

		case raw == "\xa9xyz": // Android / QuickTime ISO6709 location box
			if b := readRaw(h); len(b) > 4 {
				if lat, lon, ok := parseISO6709(string(b[4:])); ok {
					d.GPS = fmt.Sprintf("%.5f, %.5f", lat, lon)
				}
			}

		case raw == "loci": // 3GPP / ffmpeg location box
			if lat, lon, ok := parseLoci(readRaw(h)); ok {
				d.GPS = fmt.Sprintf("%.5f, %.5f", lat, lon)
			}

		case t == "mvhd":
			if d.Taken == "" {
				if pl, _, err := h.ReadPayload(); err == nil {
					if m, ok := pl.(*mp4.Mvhd); ok {
						if u := time.Unix(mvhdSeconds(m)-2082844800, 0).UTC(); plausible(u) {
							d.Taken = u.Format("2006-01-02 15:04:05")
						}
					}
				}
			}
		}
		return nil, nil
	})

	d.Camera = cameraName(clean(mk), clean(model))
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

// --- pdf ----------------------------------------------------------------

// readPDF pulls the author, authoring software and creation date from a PDF's
// Info dictionary.
func readPDF(blob []byte) (d Data) {
	defer func() { _ = recover() }()
	r, err := pdf.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return
	}
	info := r.Trailer().Key("Info")
	d.Creator = clean(info.Key("Author").Text())
	d.Software = pdfSoftware(clean(info.Key("Creator").Text()), clean(info.Key("Producer").Text()))
	d.Taken = pdfDate(info.Key("CreationDate").Text())
	return
}

func pdfSoftware(creator, producer string) string {
	switch {
	case creator == "":
		return producer
	case producer == "":
		return creator
	case strings.Contains(strings.ToLower(producer), strings.ToLower(creator)):
		return producer
	default:
		return creator + " / " + producer
	}
}

// pdfDate parses a PDF date string, "D:20240115093000+09'00'" style.
func pdfDate(s string) string {
	s = strings.TrimPrefix(s, "D:")
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	for _, layout := range []string{"20060102150405", "200601021504", "2006010215", "20060102"} {
		if len(layout) <= digits {
			if t, err := time.Parse(layout, s[:len(layout)]); err == nil && plausible(t) {
				return t.Format("2006-01-02 15:04:05")
			}
		}
	}
	return ""
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
		if t, err := time.Parse(layout, s); err == nil && plausible(t) {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

// plausible rejects capture dates outside the era of digital photography, which
// a corrupt or crafted timestamp field otherwise renders literally.
func plausible(t time.Time) bool {
	return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1
}
