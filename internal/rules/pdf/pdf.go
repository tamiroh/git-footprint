// Package pdf reads a committed PDF's Info dictionary and XMP packets.
package pdf

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rsc.io/pdf"

	"github.com/tamiroh/git-footprint/internal/rule"
	"github.com/tamiroh/git-footprint/internal/xmp"
)

type data struct {
	creators, annotators []string
	software, taken      string
}

func (d data) empty() bool {
	return len(d.creators) == 0 && len(d.annotators) == 0 && d.software == "" && d.taken == ""
}

type item struct {
	data
	path, link string
	by         rule.Author
}

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
		ni := len(r.items[i].creators) + len(r.items[i].annotators)
		nj := len(r.items[j].creators) + len(r.items[j].annotators)
		if (ni > 0) != (nj > 0) {
			return ni > 0
		}
		return r.items[i].path < r.items[j].path
	})

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		var checks []rule.Check
		for _, c := range it.creators {
			checks = append(checks, rule.Check{Name: "pdf-creator", Level: rule.Warn, Value: c})
		}
		for _, a := range it.annotators {
			checks = append(checks, rule.Check{Name: "pdf-contributor", Level: rule.Warn, Value: a})
		}
		checks = append(checks,
			rule.Check{Name: "pdf-software", Level: rule.Info, Value: it.software},
			rule.Check{Name: "pdf-date", Level: rule.Info, Value: it.taken},
		)
		out = append(out, rule.Finding{
			Detector: "pdf-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty(checks),
		})
	}
	return out
}

func dedupe(in []item) []item {
	type key struct{ creators, annotators, software, taken, path, name, email string }
	seen := map[key]bool{}
	var out []item
	for _, it := range in {
		k := key{
			strings.Join(it.creators, "\x00"), strings.Join(it.annotators, "\x00"),
			it.software, it.taken, it.path, it.by.Name, it.by.Email,
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

	seen := map[string]bool{}
	addCreator := func(name string) {
		if n := strings.TrimSpace(name); n != "" && !seen[n] {
			seen[n] = true
			d.creators = append(d.creators, n)
		}
	}
	annotSeen := map[string]bool{}
	addAnnotator := func(name string) {
		if n := strings.TrimSpace(name); n != "" && !seen[n] && !annotSeen[n] {
			annotSeen[n] = true
			d.annotators = append(d.annotators, n)
		}
	}

	if r, err := pdf.NewReader(bytes.NewReader(blob), int64(len(blob))); err == nil {
		info := r.Trailer().Key("Info")
		addCreator(clean(info.Key("Author").Text()))
		d.software = software(clean(info.Key("Creator").Text()), clean(info.Key("Producer").Text()))
		d.taken = date(info.Key("CreationDate").Text())

		for i := 1; i <= r.NumPage() && i <= 5000; i++ {
			annots := r.Page(i).V.Key("Annots")
			for j := 0; j < annots.Len(); j++ {
				addAnnotator(annots.Index(j).Key("T").Text())
			}
		}
	}

	for _, x := range xmp.All(blob) {
		addCreator(x.Creator)
		if d.software == "" {
			d.software = x.Tool
		}
		if d.taken == "" {
			d.taken = x.Date
		}
	}
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

// date: "D:20240115093000+09'00'"
func date(s string) string {
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

func clean(s string) string { return strings.TrimRight(s, "\x00 ") }

func plausible(t time.Time) bool { return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1 }
