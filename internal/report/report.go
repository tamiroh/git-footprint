// Package report renders the footprint as a per-contributor terminal report.
package report

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/rule"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiYellow = "\x1b[33m"
)

type painter struct {
	w     io.Writer
	color bool
}

func (pt painter) link(text, target string) string { // OSC 8 hyperlink
	if !pt.color || target == "" {
		return text
	}
	uri := (&url.URL{Scheme: "file", Path: target}).String()
	return "\x1b]8;;" + uri + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func (pt painter) put(text string, codes ...string) {
	var active []string
	for _, c := range codes {
		if c != "" {
			active = append(active, c)
		}
	}
	if pt.color && len(active) > 0 {
		fmt.Fprint(pt.w, strings.Join(active, "")+text+ansiReset)
	} else {
		fmt.Fprint(pt.w, text)
	}
}

func plural(n int, one, many string) string {
	form := many
	if n == 1 {
		form = one
	}
	return strings.ReplaceAll(form, "$1", fmt.Sprint(n))
}

// commitCount always shows both roles, so the column reads uniformly.
func commitCount(id identity.Identity) string {
	return fmt.Sprintf("authored %d · committed %d", id.AuthorCommits, id.CommitterCommits)
}

func dateRange(id identity.Identity) string {
	switch {
	case len(id.FirstDate) == 10 && id.FirstDate != id.LastDate:
		return id.FirstDate + " -> " + id.LastDate
	case len(id.FirstDate) == 10:
		return id.FirstDate
	default:
		return ""
	}
}

func termWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && (r <= 0x115F ||
			(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE4F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x1F300 && r <= 0x1FAFF) ||
			(r >= 0x20000 && r <= 0x3FFFD)):
			w += 2
		default:
			w++
		}
	}
	return w
}

func headerBox(pt painter, title string, lines ...string) {
	all := append([]string{title}, lines...)
	inner := 0
	for _, l := range all {
		if n := termWidth(l); n > inner {
			inner = n
		}
	}
	rule := strings.Repeat("─", inner+2)
	pt.put("╭"+rule+"╮\n", ansiDim)
	for i, l := range all {
		code := ""
		if i == 0 {
			code = ansiBold
		}
		pt.put("│ ", ansiDim)
		pt.put(l, code)
		pt.put(strings.Repeat(" ", inner-termWidth(l))+" │\n", ansiDim)
	}
	pt.put("╰"+rule+"╯\n\n", ansiDim)
}

// ruleRank fixes the order findings from different rules appear in, both under a
// contributor and in the orphan section.
var ruleRank = map[string]int{"metadata": 0, "dsstore": 1}

// orphanTitle names the section for findings whose introducing author is not a
// listed identity (they came in through a merge).
var orphanTitle = map[string]string{
	"metadata": "media not tied to a listed identity",
	"dsstore":  ".DS_Store files not tied to a listed identity",
}

// Render writes the footprint report for fp and res to w.
func Render(w io.Writer, fp identity.Footprint, res rule.Result, repo string, color bool) {
	pt := painter{w: w, color: color}

	headerBox(pt,
		"git-footprint",
		repo,
		plural(fp.TotalCommits, "$1 commit", "$1 commits")+" across "+
			plural(len(fp.Identities), "$1 identity", "$1 identities"),
	)

	byWho := map[[2]string][]rule.Finding{}
	for _, f := range res.Findings {
		k := [2]string{f.By.Name, f.By.Email}
		byWho[k] = append(byWho[k], f)
	}

	for _, id := range fp.Identities {
		nameCode, code := ansiBold, ""
		if id.Bot {
			nameCode, code = ansiDim, ansiDim
		}
		pt.put("● "+id.Name+" <"+id.Email+">\n", nameCode)

		line := "    " + commitCount(id)
		if dr := dateRange(id); dr != "" {
			line += "  ·  " + dr
		}
		pt.put(line+"\n", code)

		k := [2]string{id.Name, id.Email}
		for _, f := range sortFindings(byWho[k]) {
			findingBlock(pt, f)
		}
		delete(byWho, k)
		pt.put("\n")
	}

	var orphans []rule.Finding
	for _, fs := range byWho {
		orphans = append(orphans, fs...)
	}
	for _, name := range []string{"metadata", "dsstore"} {
		sub := ofRule(orphans, name)
		if len(sub) == 0 {
			continue
		}
		sort.SliceStable(sub, func(i, j int) bool { return sub[i].Path < sub[j].Path })
		pt.put("\n"+orphanTitle[name]+"\n", ansiBold)
		for _, f := range sub {
			findingBlock(pt, f)
		}
	}

	recapMetadata(pt, ofRule(res.Findings, "metadata"))
	recapDSStore(pt, ofRule(res.Findings, "dsstore"))
	if n := total(res.Unclaimed); n > 0 {
		recap(pt, false, plural(n, "$1 file", "$1 files")+
			" not read (unsupported format)  ·  "+extBreakdown(res.Unclaimed))
	}
}

func sortFindings(in []rule.Finding) []rule.Finding {
	out := append([]rule.Finding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := ruleRank[out[i].Rule], ruleRank[out[j].Rule]; a != b {
			return a < b
		}
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level // Warn before Info
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func ofRule(in []rule.Finding, name string) []rule.Finding {
	var out []rule.Finding
	for _, f := range in {
		if f.Rule == name {
			out = append(out, f)
		}
	}
	return out
}

func findingBlock(pt painter, f rule.Finding) {
	label, labelCode, lineCode := "[INFO]", ansiDim, ""
	if f.Level == rule.Warn {
		label, labelCode, lineCode = "[WARN]", ansiYellow, ansiYellow
	}
	pt.put("    "+label+"  ", labelCode)
	pt.put(pt.link(f.Path, f.Link)+"\n", lineCode)

	for _, fld := range f.Detail {
		if fld.Label == "" {
			pt.put("        " + fld.Value + "\n")
			continue
		}
		pt.put("        "+fld.Label+strings.Repeat(" ", 10-len(fld.Label)), ansiDim)
		pt.put(fld.Value + "\n")
	}
}

// recap prints a bottom-of-report summary line, tagged [WARN] or [INFO].
func recap(pt painter, warn bool, text string) {
	tag, code := "[INFO]  ", ansiDim
	if warn {
		tag, code = "[WARN]  ", ansiYellow
	}
	pt.put(tag, code)
	pt.put(text+"\n", code)
}

func recapMetadata(pt painter, fs []rule.Finding) {
	if len(fs) == 0 {
		return
	}
	revealing := 0
	for _, f := range fs {
		if f.Level == rule.Warn {
			revealing++
		}
	}
	line := plural(len(fs), "$1 file carries embedded metadata", "$1 files carry embedded metadata")
	if revealing > 0 {
		recap(pt, true, line+" ("+plural(revealing, "$1 reveals", "$1 reveal")+" a location or creator)")
	} else {
		recap(pt, false, line)
	}
}

func recapDSStore(pt painter, fs []rule.Finding) {
	if len(fs) == 0 {
		return
	}
	names := 0
	for _, f := range fs {
		names += f.Count
	}
	recap(pt, true, plural(len(fs), "$1 committed .DS_Store", "$1 committed .DS_Store files")+
		" leaking "+plural(names, "$1 file/folder name", "$1 file/folder names"))
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func extBreakdown(m map[string]int) string {
	type kv struct {
		ext string
		n   int
	}
	kvs := make([]kv, 0, len(m))
	for e, n := range m {
		kvs = append(kvs, kv{e, n})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].ext < kvs[j].ext
	})
	parts := make([]string, len(kvs))
	for i, k := range kvs {
		ext := k.ext
		if ext == "" {
			ext = "(no ext)"
		}
		parts[i] = fmt.Sprintf("%s ×%d", ext, k.n)
	}
	return strings.Join(parts, "  ·  ")
}
