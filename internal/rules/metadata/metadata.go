// Package metadata is the rule that reads embedded metadata (EXIF, QuickTime
// atoms, the PDF Info dictionary) from committed image, video and PDF blobs.
package metadata

import (
	"sort"

	"github.com/tamiroh/git-footprint/internal/rule"
)

type item struct {
	Data
	path string
	link string
	by   rule.Author
}

// Rule accumulates one item per media blob that carried metadata.
type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Name() string { return "metadata" }

func (r *Rule) Wants(name string) bool { return handles(name) }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	switch {
	case inert(b.Name):
		ctx.Claim() // recognised, nothing to read (icon files)
	case handles(b.Name):
		ctx.Claim()
		if d := read(b.Name, b.Content); !d.Empty() {
			r.items = append(r.items, item{d, b.Path, ctx.Link(b, true), b.By})
		}
	}
}

func (r *Rule) Findings() []rule.Finding {
	r.items = dedupe(r.items)
	sort.SliceStable(r.items, func(i, j int) bool {
		if a, b := r.items[i].Revealing(), r.items[j].Revealing(); a != b {
			return a
		}
		return r.items[i].path < r.items[j].path
	})

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		level := rule.Info
		if it.Revealing() {
			level = rule.Warn
		}
		var detail []rule.Field
		for _, f := range []rule.Field{
			{Label: "location", Value: it.GPS},
			{Label: "creator", Value: it.Creator},
			{Label: "device", Value: it.Camera},
			{Label: "software", Value: it.Software},
			{Label: "date", Value: it.Taken},
		} {
			if f.Value != "" {
				detail = append(detail, f)
			}
		}
		out = append(out, rule.Finding{
			Rule: "metadata", Level: level, Path: it.path, Link: it.link,
			By: it.by, Detail: detail,
		})
	}
	return out
}

func dedupe(in []item) []item {
	type key struct{ path, name, email, gps, creator, camera, software, taken string }
	seen := map[key]bool{}
	var out []item
	for _, it := range in {
		k := key{it.path, it.by.Name, it.by.Email, it.GPS, it.Creator, it.Camera, it.Software, it.Taken}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}
