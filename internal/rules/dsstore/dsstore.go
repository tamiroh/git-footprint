// Package dsstore is the rule that reports the file and folder names a
// committed macOS .DS_Store leaks.
package dsstore

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/rule"
)

type item struct {
	path  string
	link  string
	by    rule.Author
	names []string
}

type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return filepath.Base(name) == ".DS_Store" }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	if filepath.Base(b.Name) != ".DS_Store" {
		return
	}
	ctx.Claim()
	if names := parseNames(b.Content); len(names) > 0 {
		r.items = append(r.items, item{b.Path, ctx.Link(b, false), b.By, names})
	}
}

func (r *Rule) Findings() []rule.Finding {
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].path < r.items[j].path })

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, rule.Finding{
			Detector: "ds-store", Path: it.path, Link: it.link, By: it.by,
			Checks: []rule.Check{{Name: "ds-store-names", Level: rule.Warn, Value: nameList(it.names)}},
			Count:  len(it.names),
		})
	}
	return out
}

func nameList(names []string) string {
	const show = 8
	head := names
	if len(head) > show {
		head = head[:show]
	}
	s := strings.Join(head, ", ")
	if len(names) > show {
		s += fmt.Sprintf(", +%d more", len(names)-show)
	}
	return s
}
