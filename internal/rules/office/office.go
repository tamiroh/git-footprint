// Package office is the rule that reads the author, editor, company and
// authoring application from a committed OOXML document (.docx / .xlsx / .pptx),
// which store them as XML in docProps/.
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
	path, link                                       string
	by                                               rule.Author
	creator, lastMod, app, company, manager, created string
}

type Rule struct{ items []item }

func New() *Rule { return &Rule{} }

func (r *Rule) Name() string { return "office" }

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
				Application string `xml:"Application"`
				AppVersion  string `xml:"AppVersion"`
				Company     string `xml:"Company"`
				Manager     string `xml:"Manager"`
			}
			if unmarshalEntry(f, &a) {
				it.app = strings.TrimSpace(a.Application + " " + a.AppVersion)
				it.company, it.manager = a.Company, a.Manager
			}
		}
	}
	if it.creator == "" && it.lastMod == "" && it.app == "" && it.company == "" && it.manager == "" {
		return
	}
	it.path, it.by, it.link = b.Path, b.By, ctx.Link(b, true)
	r.items = append(r.items, it)
}

func (r *Rule) Findings() []rule.Finding {
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].path < r.items[j].path })

	out := make([]rule.Finding, 0, len(r.items))
	for _, it := range r.items {
		creator := firstNonEmpty(it.creator, it.lastMod)
		level := rule.Info
		if creator != "" || it.company != "" || it.manager != "" {
			level = rule.Warn
		}
		var detail []rule.Field
		for _, f := range []rule.Field{
			{Label: "creator", Value: creator},
			{Label: "company", Value: strings.TrimSpace(it.company)},
			{Label: "manager", Value: strings.TrimSpace(it.manager)},
			{Label: "software", Value: it.app},
			{Label: "date", Value: officeDate(it.created)},
		} {
			if f.Value != "" {
				detail = append(detail, f)
			}
		}
		out = append(out, rule.Finding{
			Rule: "office", Level: level, Path: it.path, Link: it.link, By: it.by, Detail: detail,
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
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20)) // docProps XML is tiny
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

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
