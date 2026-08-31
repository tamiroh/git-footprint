package dsstore

// A .DS_Store is a buddy-allocator container holding a B-tree of Finder records
// keyed by name. Those names — every sibling Finder saw, deleted ones included —
// plus any Finder comment and the disk path of a folder-background image are
// what leak.

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
	"unicode/utf16"
)

type dsData struct {
	names    []string
	comments []string
	paths    []string
}

func parse(b []byte) (out dsData) {
	defer func() { _ = recover() }()

	if len(b) < 36 || string(b[4:8]) != "Bud1" {
		return
	}
	be := binary.BigEndian

	book := int(be.Uint32(b[8:])) + 4 // allocator offsets exclude the 4 magic bytes
	if book < 0 || book+8 > len(b) {
		return
	}
	nBlocks := int(be.Uint32(b[book:]))
	p := book + 8

	if nBlocks < 0 || nBlocks > len(b)/4 {
		return
	}
	addr := make([]uint32, 0, nBlocks)
	for i := 0; i < nBlocks; i++ {
		if p+4 > len(b) {
			return
		}
		addr = append(addr, be.Uint32(b[p:]))
		p += 4
	}
	p += ((nBlocks+255)/256*256 - nBlocks) * 4 // table padded to a multiple of 256

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
	visited := map[int]bool{} // a crafted cycle would otherwise not terminate
	var walk func(node, depth int)
	walk = func(node, depth int) {
		if depth > 40 || visited[node] {
			return
		}
		visited[node] = true
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
			structID := string(d[pos+nl*2 : pos+nl*2+4])
			pos += nl*2 + 4
			dtype := string(d[pos : pos+4])
			pos += 4

			val := pos
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
				out.names = append(out.names, name)
			}
			switch {
			case structID == "cmmt" && dtype == "ustr":
				if c := strings.TrimSpace(decodeUTF16BE(d[val+4 : pos])); c != "" {
					out.comments = append(out.comments, c)
				}
			case dtype == "blob":
				out.paths = append(out.paths, scanPaths(d[val+4:pos])...)
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
		if internal {
			walk(int(be.Uint32(d[0:])), depth+1) // rightmost child = the header's P field
		}
	}
	walk(int(be.Uint32(head)), 0)

	sort.Strings(out.names)
	return
}

// scanPaths pulls user-home paths out of a raw value — Finder stores a
// background image as an alias/bookmark blob with the path in plain bytes.
func scanPaths(b []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range []string{"/Users/", "/home/"} {
		for rest := b; ; {
			j := bytes.Index(rest, []byte(tag))
			if j < 0 {
				break
			}
			e := j
			for e < len(rest) && rest[e] >= 0x20 && rest[e] < 0x7f {
				e++
			}
			if p := string(rest[j:e]); len(p) > len(tag) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			rest = rest[e:]
		}
	}
	return out
}

func decodeUTF16BE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}
