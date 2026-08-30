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

func (r *Rule) Name() string { return "dsstore" }

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
			Rule: "dsstore", Level: rule.Warn, Path: it.path, Link: it.link, By: it.by,
			Detail: []rule.Field{{Label: "", Value: nameList(it.names)}},
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
	noun := "names"
	if len(names) == 1 {
		noun = "name"
	}
	s := fmt.Sprintf("%d %s: %s", len(names), noun, strings.Join(head, ", "))
	if len(names) > show {
		s += fmt.Sprintf(", +%d more", len(names)-show)
	}
	return s
}
