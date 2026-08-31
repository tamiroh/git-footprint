// Package archive is the rule that looks inside committed zip archives (which
// includes .docx, .jar, .crx and friends), feeding every entry back through the
// other rules.
package archive

import (
	"archive/zip"
	"bytes"
	"io"

	"github.com/tamiroh/git-footprint/internal/rule"
)

const (
	maxEntry = 64 << 20  // largest entry to decompress
	maxTotal = 512 << 20 // decompression budget per archive
)

type Rule struct{}

func New() *Rule { return &Rule{} }

func (Rule) Findings() []rule.Finding { return nil } // it only feeds other rules

func (Rule) Visit(ctx rule.Context, b rule.Blob) {
	if !isZip(b.Content) {
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Content), int64(len(b.Content)))
	if err != nil {
		return // magic only: let it fall through to the unread tally
	}
	ctx.Claim()

	budget := int64(maxTotal)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > maxEntry || !ctx.Wants(f.Name) {
			continue
		}
		data, err := readEntry(f)
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

func isZip(b []byte) bool {
	return bytes.HasPrefix(b, []byte("PK\x03\x04")) || bytes.HasPrefix(b, []byte("PK\x05\x06"))
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxEntry))
}
