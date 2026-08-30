// Package exif extracts the identifying EXIF fields from an image blob.
package exif

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/evanoberholster/imagemeta"
)

var exts = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true,
	".png": true, ".tif": true, ".tiff": true, ".dng": true,
	".heic": true, ".heif": true, ".avif": true,
	".cr2": true, ".cr3": true, ".crw": true, ".arw": true, ".nef": true,
}

// IsImage reports whether path has an extension exif can read.
func IsImage(path string) bool {
	return exts[strings.ToLower(filepath.Ext(path))]
}

type Data struct {
	GPS     string // "lat, long"
	Creator string // Artist, else Copyright
	Camera  string // "Make Model"
	Taken   string // "2006-01-02 15:04:05"
}

func (d Data) Empty() bool {
	return d.GPS == "" && d.Creator == "" && d.Camera == "" && d.Taken == ""
}

func (d Data) Revealing() bool { return d.GPS != "" || d.Creator != "" }

// Read pulls EXIF from an image blob. Anything unexpected yields a zero Data.
func Read(blob []byte) (d Data) {
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
	if t := x.OriginalDate(); !t.IsZero() {
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
