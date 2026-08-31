// Package archive feeds the entries of a committed zip or tar back through the
// rules, and reports the owner name a tar header carries.
package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"sort"

	"github.com/tamiroh/git-footprint/internal/rule"
)

const (
	maxEntry = 64 << 20
	maxTotal = 512 << 20 // decompression budget per archive
)

type arc struct {
	path, link string
	by         rule.Author
	owners     []string
}

type Rule struct{ arcs []arc }

func New() *Rule { return &Rule{} }

func (r *Rule) Findings() []rule.Finding {
	sort.SliceStable(r.arcs, func(i, j int) bool { return r.arcs[i].path < r.arcs[j].path })

	out := make([]rule.Finding, 0, len(r.arcs))
	for _, a := range r.arcs {
		var checks []rule.Check
		for _, name := range a.owners {
			checks = append(checks, rule.Check{Name: "archive-owner", Level: rule.Warn, Value: name})
		}
		out = append(out, rule.Finding{
			Detector: "archive", Path: a.path, Link: a.link, By: a.by, Checks: checks,
		})
	}
	return out
}

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	switch {
	case isZip(b.Content):
		visitZip(ctx, b)
	case bytes.HasPrefix(b.Content, []byte{0x1f, 0x8b}):
		if gz, err := gzip.NewReader(bytes.NewReader(b.Content)); err == nil {
			r.visitTar(ctx, b, gz)
		}
	case isTar(b.Content):
		r.visitTar(ctx, b, bytes.NewReader(b.Content))
	}
}

func visitZip(ctx rule.Context, b rule.Blob) {
	zr, err := zip.NewReader(bytes.NewReader(b.Content), int64(len(b.Content)))
	if err != nil {
		return // magic matched but it won't open: leave it unclaimed
	}
	ctx.Claim()

	budget := int64(maxTotal)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > maxEntry || !ctx.Wants(f.Name) {
			continue
		}
		data, err := readZipEntry(f)
		if err != nil {
			continue
		}
		if budget -= int64(len(data)); budget < 0 {
			break
		}
		ctx.Inspect(rule.Blob{
			Path: b.Path + " » " + f.Name, Name: f.Name,
			Content: data, By: b.By, SHA: b.SHA,
		})
	}
}

func (r *Rule) visitTar(ctx rule.Context, b rule.Blob, src io.Reader) {
	tr := tar.NewReader(src)
	h, err := tr.Next()
	if err != nil {
		return // not a tar, or empty
	}
	ctx.Claim()

	seen := map[string]bool{}
	var owners []string
	budget := int64(maxTotal)
	for ; err == nil; h, err = tr.Next() {
		if h.Uname != "" && !seen[h.Uname] {
			seen[h.Uname] = true
			owners = append(owners, h.Uname)
		}
		if h.FileInfo().IsDir() || h.Size > maxEntry || !ctx.Wants(h.Name) {
			continue
		}
		data, e := io.ReadAll(io.LimitReader(tr, maxEntry))
		if e != nil {
			continue
		}
		if budget -= int64(len(data)); budget < 0 {
			break
		}
		ctx.Inspect(rule.Blob{
			Path: b.Path + " » " + h.Name, Name: h.Name,
			Content: data, By: b.By, SHA: b.SHA,
		})
	}
	if len(owners) > 0 {
		r.arcs = append(r.arcs, arc{b.Path, ctx.Link(b, false), b.By, owners})
	}
}

func isZip(b []byte) bool {
	return bytes.HasPrefix(b, []byte("PK\x03\x04")) || bytes.HasPrefix(b, []byte("PK\x05\x06"))
}

func isTar(b []byte) bool {
	return len(b) >= 265 && string(b[257:262]) == "ustar"
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxEntry))
}
