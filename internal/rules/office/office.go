// Package office reads docProps/ metadata from a committed .docx/.xlsx/.pptx.
package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamiroh/git-footprint/internal/rule"
)

var exts = map[string]bool{".docx": true, ".xlsx": true, ".pptx": true}

type item struct {
	path, link                                                  string
	by                                                          rule.Author
	creator, lastMod, app, company, manager, hlinkBase, created string
	custom                                                      []customProp
	contributors                                                []string
}

type customProp struct {
	text string
	leak bool
}

type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Wants(name string) bool { return exts[strings.ToLower(filepath.Ext(name))] }

func (r *Rule) Visit(ctx rule.Context, b rule.Blob) {
	if !r.Wants(b.Name) {
		return
	}
	ctx.Claim()
	zr, err := zip.NewReader(bytes.NewReader(b.Content), int64(len(b.Content)))
	if err != nil {
		return
	}

	var it item
	for _, f := range zr.File {
		switch {
		case f.Name == "docProps/core.xml":
			var c struct {
				Creator        string `xml:"creator"`
				LastModifiedBy string `xml:"lastModifiedBy"`
				Created        string `xml:"created"`
			}
			if unmarshalEntry(f, &c) {
				it.creator, it.lastMod, it.created = c.Creator, c.LastModifiedBy, c.Created
			}
		case f.Name == "docProps/app.xml":
			var a struct {
				Application   string `xml:"Application"`
				AppVersion    string `xml:"AppVersion"`
				Company       string `xml:"Company"`
				Manager       string `xml:"Manager"`
				HyperlinkBase string `xml:"HyperlinkBase"`
			}
			if unmarshalEntry(f, &a) {
				it.app = strings.TrimSpace(a.Application + " " + a.AppVersion)
				it.company, it.manager, it.hlinkBase = a.Company, a.Manager, a.HyperlinkBase
			}
		case f.Name == "docProps/custom.xml":
			var c struct {
				Props []struct {
					Name string `xml:"name,attr"`
					Str  string `xml:"lpwstr"`
				} `xml:"property"`
			}
			if unmarshalEntry(f, &c) {
				for _, p := range c.Props {
					if v := strings.TrimSpace(p.Str); v != "" {
						it.custom = append(it.custom, customProp{p.Name + " = " + v, leaky(v)})
					}
				}
			}
		case hasAuthors(f.Name):
			it.contributors = append(it.contributors, entryAuthors(f)...)
		}
	}
	if it.creator == "" && it.lastMod == "" && it.app == "" && it.company == "" && it.manager == "" &&
		it.hlinkBase == "" && len(it.custom) == 0 && len(it.contributors) == 0 {
		return
	}
	it.path, it.by, it.link = b.Path, b.By, ctx.Link(b, true)
	r.items = append(r.items, it)
}

func (r *Rule) Findings() []rule.Finding {
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].path < r.items[j].path })

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		checks := []rule.Check{
			{Name: "office-author", Level: rule.Warn, Value: strings.TrimSpace(it.creator)},
			{Name: "office-editor", Level: rule.Warn, Value: strings.TrimSpace(it.lastMod)},
			{Name: "office-company", Level: rule.Warn, Value: strings.TrimSpace(it.company)},
			{Name: "office-manager", Level: rule.Warn, Value: strings.TrimSpace(it.manager)},
			{Name: "office-path", Level: rule.Warn, Value: strings.TrimSpace(it.hlinkBase)},
		}
		checks = append(checks, contributorChecks(it)...)
		checks = append(checks, customChecks(it.custom)...)
		checks = append(checks,
			rule.Check{Name: "office-application", Level: rule.Info, Value: it.app},
			rule.Check{Name: "office-date", Level: rule.Info, Value: officeDate(it.created)},
		)
		out = append(out, rule.Finding{
			Detector: "office-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty(checks),
		})
	}
	return out
}

func unmarshalEntry(f *zip.File, v any) bool {
	rc, err := f.Open()
	if err != nil {
		return false
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return false
	}
	return xml.Unmarshal(data, v) == nil
}

func officeDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ""
}

func contributorChecks(it item) []rule.Check {
	seen := map[string]bool{
		strings.TrimSpace(it.creator): true,
		strings.TrimSpace(it.lastMod): true,
	}
	names := append([]string(nil), it.contributors...)
	sort.Strings(names)

	var out []rule.Check
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, rule.Check{Name: "office-contributor", Level: rule.Warn, Value: name})
		}
	}
	return out
}

// hasAuthors is true for the OOXML parts that name comment or tracked-change
// authors: word/, and the xlsx / pptx author lists.
func hasAuthors(name string) bool {
	switch name {
	case "word/comments.xml", "word/document.xml", "word/people.xml",
		"ppt/commentAuthors.xml", "ppt/authors.xml":
		return true
	}
	return strings.HasPrefix(name, "xl/comments") || strings.HasPrefix(name, "xl/persons/")
}

func entryAuthors(f *zip.File) []string {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	dec := xml.NewDecoder(io.LimitReader(rc, 8<<20))
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}

	inAuthorEl := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "ins", "del", "comment": // word: w:author attribute
				add(attr(t, "author"))
			case "person": // word w15:person, or xlsx person
				add(attr(t, "author"))
				add(attr(t, "displayName"))
			case "cmAuthor": // pptx commentAuthors
				add(attr(t, "name"))
			case "author": // pptx authors (name attr) or xlsx comments (element text)
				add(attr(t, "name"))
				inAuthorEl = true
			}
		case xml.CharData:
			if inAuthorEl {
				add(string(t))
			}
		case xml.EndElement:
			if t.Name.Local == "author" {
				inAuthorEl = false
			}
		}
	}
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func customChecks(props []customProp) []rule.Check {
	sort.SliceStable(props, func(i, j int) bool { return props[i].leak && !props[j].leak })

	const show = 8
	out := make([]rule.Check, 0, len(props))
	for i, p := range props {
		if i == show {
			out = append(out, rule.Check{
				Name: "office-custom", Level: rule.Info,
				Value: fmt.Sprintf("+%d more custom properties", len(props)-show),
			})
			break
		}
		level := rule.Info
		if p.leak {
			level = rule.Warn
		}
		out = append(out, rule.Check{Name: "office-custom", Level: level, Value: p.text})
	}
	return out
}

func leaky(s string) bool {
	switch {
	case strings.Contains(s, "://"):
		return true
	case strings.Contains(s, `\\`), strings.Contains(s, `:\`),
		strings.Contains(s, "/Users/"), strings.Contains(s, "/home/"):
		return true
	}
	at := strings.IndexByte(s, '@')
	return at > 0 && !strings.ContainsAny(s, " \t") && strings.LastIndexByte(s, '.') > at
}
