// Package pdf is the rule that reads the author, authoring software and creation
// date from a committed PDF's Info dictionary.
package pdf

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rsc.io/pdf"

	"github.com/tamiroh/git-footprint/internal/mediameta"
	"github.com/tamiroh/git-footprint/internal/rule"
)

type data struct{ creator, software, taken string }

func (d data) empty() bool { return d == data{} }

type item struct {
	data
	path, link string
	by         rule.Author
}

// Rule accumulates one item per PDF blob that carried metadata.
type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return strings.ToLower(filepath.Ext(name)) == ".pdf" }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	if !r.Wants(b.Name) {
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
		if a, b := r.items[i].creator != "", r.items[j].creator != ""; a != b {
			return a
		}
		return r.items[i].path < r.items[j].path
	})

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, rule.Finding{
			Detector: "pdf-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty([]rule.Check{
				{Name: "pdf-creator", Level: rule.Warn, Value: it.creator},
				{Name: "pdf-software", Level: rule.Info, Value: it.software},
				{Name: "pdf-date", Level: rule.Info, Value: it.taken},
			}),
		})
	}
	return out
}

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
	defer func() { _ = recover() }()
	r, err := pdf.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return
	}
	info := r.Trailer().Key("Info")
	d.creator = mediameta.Clean(info.Key("Author").Text())
	d.software = software(mediameta.Clean(info.Key("Creator").Text()), mediameta.Clean(info.Key("Producer").Text()))
	d.taken = date(info.Key("CreationDate").Text())
	return
}

func software(creator, producer string) string {
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

// date parses a PDF date string, "D:20240115093000+09'00'" style.
func date(s string) string {
	s = strings.TrimPrefix(s, "D:")
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	for _, layout := range []string{"20060102150405", "200601021504", "2006010215", "20060102"} {
		if len(layout) <= digits {
			if t, err := time.Parse(layout, s[:len(layout)]); err == nil && mediameta.Plausible(t) {
				return t.Format("2006-01-02 15:04:05")
			}
		}
	}
	return ""
}
