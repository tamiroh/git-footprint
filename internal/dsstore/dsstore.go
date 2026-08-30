// Package dsstore reads the file and folder names recorded in a macOS
// .DS_Store file.
//
// A .DS_Store is a "buddy allocator" container holding a B-tree of Finder
// view-setting records, one or more per name. The names are what leaks: every
// sibling file and folder that was present when Finder last wrote the file,
// including ones since deleted.
package dsstore

import (
	"encoding/binary"
	"sort"
	"unicode/utf16"
)

// Names returns the distinct file/folder names a .DS_Store recorded, sorted.
// Anything unexpected in the bytes yields nil rather than an error or panic.
func Names(b []byte) (names []string) {
	defer func() { _ = recover() }()

	if len(b) < 36 || string(b[4:8]) != "Bud1" {
		return
	}
	be := binary.BigEndian

	// The buddy allocator's offsets exclude the leading 4 magic bytes.
	book := int(be.Uint32(b[8:])) + 4
	if book < 0 || book+8 > len(b) {
		return
	}
	nBlocks := int(be.Uint32(b[book:]))
	p := book + 8

	addr := make([]uint32, 0, nBlocks)
	for i := 0; i < nBlocks; i++ {
		if p+4 > len(b) {
			return
		}
		addr = append(addr, be.Uint32(b[p:]))
		p += 4
	}
	p += ((nBlocks+255)/256*256 - nBlocks) * 4 // the address table is padded to a multiple of 256

	if p+4 > len(b) {
		return
	}
	nDir := int(be.Uint32(b[p:]))
	p += 4
	dsdb := -1
	for i := 0; i < nDir; i++ {
		if p+1 > len(b) {
			return
		}
		nl := int(b[p])
		p++
		if p+nl+4 > len(b) {
			return
		}
		if string(b[p:p+nl]) == "DSDB" {
			dsdb = int(be.Uint32(b[p+nl:]))
		}
		p += nl + 4
	}
	if dsdb < 0 || dsdb >= len(addr) {
		return
	}

	block := func(n int) []byte {
		if n < 0 || n >= len(addr) {
			return nil
		}
		v := addr[n]
		off := int(v&^uint32(0x1f)) + 4
		size := 1 << (v & 0x1f)
		if off < 0 || off+size > len(b) {
			return nil
		}
		return b[off : off+size]
	}

	head := block(dsdb)
	if len(head) < 4 {
		return
	}

	seen := map[string]bool{}
	var walk func(node, depth int)
	walk = func(node, depth int) {
		if depth > 40 {
			return
		}
		d := block(node)
		if len(d) < 8 {
			return
		}
		internal := be.Uint32(d[0:]) != 0
		count := int(be.Uint32(d[4:]))
		pos := 8

		record := func() bool {
			if pos+4 > len(d) {
				return false
			}
			nl := int(be.Uint32(d[pos:]))
			pos += 4
			if nl < 0 || pos+nl*2+8 > len(d) {
				return false
			}
			name := decodeUTF16BE(d[pos : pos+nl*2])
			pos += nl*2 + 4 // name + 4-byte structure id
			dtype := string(d[pos : pos+4])
			pos += 4
			switch dtype {
			case "long", "shor", "type":
				pos += 4
			case "bool":
				pos++
			case "comp", "dutc":
				pos += 8
			case "blob", "ustr":
				if pos+4 > len(d) {
					return false
				}
				vlen := int(be.Uint32(d[pos:]))
				pos += 4
				if dtype == "ustr" {
					vlen *= 2
				}
				pos += vlen
			default:
				return false
			}
			if pos > len(d) {
				return false
			}
			if name != "" && name != "." && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			return true
		}

		for i := 0; i < count; i++ {
			if internal {
				if pos+4 > len(d) {
					return
				}
				walk(int(be.Uint32(d[pos:])), depth+1)
				pos += 4
			}
			if !record() {
				return
			}
		}
		if internal && pos+4 <= len(d) {
			walk(int(be.Uint32(d[pos:])), depth+1)
		}
	}
	walk(int(be.Uint32(head)), 0)

	sort.Strings(names)
	return
}

func decodeUTF16BE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}
