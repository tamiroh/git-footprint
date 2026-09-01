// Package font reads the name table of a committed TrueType/OpenType/WOFF font.
package font

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/rule"
)

var exts = map[string]bool{
	".ttf": true, ".otf": true, ".ttc": true, ".otc": true, ".woff": true,
}

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }

type data struct{ copyright, vendor, designer string }

func (d data) empty() bool { return d == data{} }

type item struct {
	data
	path, link string
	by         rule.Author
}

type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return exts[ext(name)] }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	if !exts[ext(b.Name)] {
		return
	}
	ctx.Claim()
	if d := parse(b.Content); !d.empty() {
		r.items = append(r.items, item{d, b.Path, ctx.Link(b, true), b.By})
	}
}

func (r *Rule) Findings() []rule.Finding {
	r.items = dedupe(r.items)
	sort.SliceStable(r.items, func(i, j int) bool {
		if a, b := r.items[i].designer != "", r.items[j].designer != ""; a != b {
			return a
		}
		return r.items[i].path < r.items[j].path
	})

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, rule.Finding{
			Detector: "font-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty([]rule.Check{
				{Name: "font-designer", Level: rule.Info, Value: it.designer},
				{Name: "font-vendor", Level: rule.Info, Value: it.vendor},
				{Name: "font-copyright", Level: rule.Info, Value: it.copyright},
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
