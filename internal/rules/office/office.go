// Package office reads docProps/ metadata from a committed .docx/.xlsx/.pptx.
package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
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
		}
	}
	if it.creator == "" && it.lastMod == "" && it.app == "" &&
		it.company == "" && it.manager == "" && it.hlinkBase == "" {
		return
	}
	it.path, it.by, it.link = b.Path, b.By, ctx.Link(b, true)
	r.items = append(r.items, it)
}

func (r *Rule) Findings() []rule.Finding {
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].path < r.items[j].path })

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, rule.Finding{
			Detector: "office-metadata", Path: it.path, Link: it.link, By: it.by,
			Checks: rule.NonEmpty([]rule.Check{
				{Name: "office-author", Level: rule.Warn, Value: strings.TrimSpace(it.creator)},
				{Name: "office-editor", Level: rule.Warn, Value: strings.TrimSpace(it.lastMod)},
				{Name: "office-company", Level: rule.Warn, Value: strings.TrimSpace(it.company)},
				{Name: "office-manager", Level: rule.Warn, Value: strings.TrimSpace(it.manager)},
				{Name: "office-path", Level: rule.Warn, Value: strings.TrimSpace(it.hlinkBase)},
				{Name: "office-application", Level: rule.Info, Value: it.app},
				{Name: "office-date", Level: rule.Info, Value: officeDate(it.created)},
			}),
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
