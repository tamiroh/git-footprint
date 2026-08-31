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
}

type customProp struct {
	text string // "name = value"
	leak bool   // value looks like a path, address or URL
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
		switch f.Name {
		case "docProps/core.xml":
			var c struct {
				Creator        string `xml:"creator"`
				LastModifiedBy string `xml:"lastModifiedBy"`
				Created        string `xml:"created"`
			}
			if unmarshalEntry(f, &c) {
				it.creator, it.lastMod, it.created = c.Creator, c.LastModifiedBy, c.Created
			}
		case "docProps/app.xml":
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
		case "docProps/custom.xml":
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
		}
	}
	if it.creator == "" && it.lastMod == "" && it.app == "" && it.company == "" &&
		it.manager == "" && it.hlinkBase == "" && len(it.custom) == 0 {
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

// customChecks renders the custom properties, path/address/URL ones first and as
// Warn, the rest as Info, capped so a DMS-stamped file can't flood the report.
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
