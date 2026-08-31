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
		kind := "image"
		switch {
		case isVideo(it.path):
			kind = "video"
		case isDoc(it.path):
			kind = "pdf"
		}
		var checks []rule.Check
		for _, c := range []struct {
			field string
			value string
			level rule.Level
		}{
			{"location", it.GPS, rule.Warn},
			{"creator", it.Creator, rule.Warn},
			{"camera", it.Camera, rule.Info},
			{"software", it.Software, rule.Info},
			{"date", it.Taken, rule.Info},
		} {
			if c.value != "" {
				checks = append(checks, rule.Check{Name: kind + "-" + c.field, Level: c.level, Value: c.value})
			}
		}
		out = append(out, rule.Finding{
			Detector: "embedded-metadata", Path: it.path, Link: it.link, By: it.by, Checks: checks,
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
