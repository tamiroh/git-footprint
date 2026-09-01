package font

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf16"
)

func parse(blob []byte) (d data) {
	defer func() { _ = recover() }()

	var nt []byte
	switch {
	case bytes.HasPrefix(blob, []byte("wOFF")):
		nt = woffNameTable(blob)
	case bytes.HasPrefix(blob, []byte("ttcf")):
		if len(blob) >= 16 {
			nt = sfntNameTable(blob, int(be32(blob[12:]))) // first font's offset table
		}
	case bytes.HasPrefix(blob, []byte{0x00, 0x01, 0x00, 0x00}),
		bytes.HasPrefix(blob, []byte("OTTO")),
		bytes.HasPrefix(blob, []byte("true")),
		bytes.HasPrefix(blob, []byte("typ1")):
		nt = sfntNameTable(blob, 0)
	}
	if nt == nil {
		return
	}
	return readNames(nt)
}

func sfntNameTable(b []byte, base int) []byte {
	if base < 0 || base+12 > len(b) {
		return nil
	}
	for i, n := 0, int(be16(b[base+4:])); i < n; i++ {
		e := base + 12 + i*16
		if e+16 > len(b) {
			return nil
		}
		if string(b[e:e+4]) != "name" {
			continue
		}
		off, length := int(be32(b[e+8:])), int(be32(b[e+12:]))
		if off+length > len(b) {
			return nil
		}
		return b[off : off+length]
	}
	return nil
}

func woffNameTable(b []byte) []byte {
	if len(b) < 44 {
		return nil
	}
	for i, n := 0, int(be16(b[12:])); i < n; i++ {
		e := 44 + i*20
		if e+20 > len(b) {
			return nil
		}
		if string(b[e:e+4]) != "name" {
			continue
		}
		off, compLen, origLen := int(be32(b[e+4:])), int(be32(b[e+8:])), int(be32(b[e+12:]))
		if off+compLen > len(b) {
			return nil
		}
		raw := b[off : off+compLen]
		if compLen == origLen {
			return raw
		}
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil
		}
		out, err := io.ReadAll(io.LimitReader(zr, 1<<20))
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}

// readNames pulls the identity-bearing records from a 'name' table: 0 copyright,
// 8 manufacturer, 9 designer.
func readNames(nt []byte) (d data) {
	if len(nt) < 6 {
		return
	}
	count := int(be16(nt[2:]))
	strBase := int(be16(nt[4:]))

	kept := map[int]int{} // nameID -> platformID of the record we took
	for i := 0; i < count; i++ {
		r := 6 + i*12
		if r+12 > len(nt) {
			break
		}
		plat, id := int(be16(nt[r:])), int(be16(nt[r+6:]))
		field := nameField(&d, id)
		if field == nil || kept[id] == 3 {
			continue // unknown record, or we already have the Windows one
		}
		off, length := strBase+int(be16(nt[r+10:])), int(be16(nt[r+8:]))
		if off+length > len(nt) {
			continue
		}
		if v := clean(decodeName(plat, nt[off:off+length])); v != "" {
			*field, kept[id] = v, plat
		}
	}
	return
}

func nameField(d *data, id int) *string {
	switch id {
	case 0:
		return &d.copyright
	case 8:
		return &d.vendor
	case 9:
		return &d.designer
	}
	return nil
}

func decodeName(platform int, b []byte) string {
	if platform == 1 { // Macintosh: read as Latin-1, adequate for ASCII credits
		var sb strings.Builder
		for _, c := range b {
			sb.WriteRune(rune(c))
		}
		return sb.String()
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = be16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func clean(s string) string { return strings.TrimRight(strings.TrimSpace(s), "\x00 ") }

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
