// Package report renders the footprint as a per-contributor terminal report.
package report

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/scan"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
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

func commitCount(id identity.Identity) string {
	switch {
	case id.AuthorCommits == 0:
		return plural(id.CommitterCommits, "$1 commit", "$1 commits") + " (as committer only)"
	case id.CommitterCommits == 0:
		return plural(id.AuthorCommits, "$1 commit", "$1 commits") + " (as author only)"
	case id.AuthorCommits != id.CommitterCommits:
		return plural(id.AuthorCommits, "$1 commit", "$1 commits") +
			fmt.Sprintf(" (%d as committer)", id.CommitterCommits)
	default:
		return plural(id.AuthorCommits, "$1 commit", "$1 commits")
	}
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

// Render writes the footprint report for fp and s to w.
func Render(w io.Writer, fp identity.Footprint, s scan.Result, repo string, color bool) {
	pt := painter{w: w, color: color}
	media := s.Media

	headerBox(pt,
		"git-footprint",
		repo,
		plural(fp.TotalCommits, "$1 commit", "$1 commits")+" across "+
			plural(len(fp.Identities), "$1 identity", "$1 identities"),
	)

	byWho := map[[2]string][]scan.Media{}
	for _, m := range media {
		k := [2]string{m.ByName, m.ByEmail}
		byWho[k] = append(byWho[k], m)
	}
	dsByWho := map[[2]string][]scan.DSStore{}
	for _, d := range s.DSStores {
		k := [2]string{d.ByName, d.ByEmail}
		dsByWho[k] = append(dsByWho[k], d)
	}

	revealing := 0
	for _, id := range fp.Identities {
		youColor := ""
		if id.IsYou {
			youColor = ansiGreen
		}
		pt.put("● ", ansiBold, youColor)
		if id.IsYou {
			pt.put("you  ", ansiBold, ansiGreen)
		}
		pt.put(id.Name+" <"+id.Email+">\n", ansiBold)

		line := "    " + commitCount(id)
		if dr := dateRange(id); dr != "" {
			line += "  ·  " + dr
		}
		pt.put(line + "\n")

		k := [2]string{id.Name, id.Email}
		for _, m := range byWho[k] {
			if m.Revealing() {
				revealing++
			}
			mediaLine(pt, m)
		}
		for _, d := range dsByWho[k] {
			dsLine(pt, d)
		}
		delete(byWho, k)
		delete(dsByWho, k)

		pt.put("\n")
	}

	var orphan []scan.Media
	for _, ms := range byWho {
		orphan = append(orphan, ms...)
	}
	if len(orphan) > 0 {
		sort.SliceStable(orphan, func(i, j int) bool { return orphan[i].Path < orphan[j].Path })
		pt.put("\nmedia not tied to a listed identity\n", ansiBold)
		for _, m := range orphan {
			if m.Revealing() {
				revealing++
			}
			mediaLine(pt, m)
		}
	}

	var dsOrphan []scan.DSStore
	for _, ds := range dsByWho {
		dsOrphan = append(dsOrphan, ds...)
	}
	if len(dsOrphan) > 0 {
		sort.SliceStable(dsOrphan, func(i, j int) bool { return dsOrphan[i].Path < dsOrphan[j].Path })
		pt.put("\n.DS_Store files not tied to a listed identity\n", ansiBold)
		for _, d := range dsOrphan {
			dsLine(pt, d)
		}
	}

	if len(media) > 0 {
		line := plural(len(media), "$1 file carries embedded metadata", "$1 files carry embedded metadata")
		if revealing > 0 {
			recap(pt, true, line+" ("+plural(revealing, "$1 reveals", "$1 reveal")+" a location or creator)")
		} else {
			recap(pt, false, line)
		}
	}

	if n := len(s.DSStores); n > 0 {
		nameCount := 0
		for _, d := range s.DSStores {
			nameCount += len(d.Names)
		}
		recap(pt, true, plural(n, "$1 committed .DS_Store", "$1 committed .DS_Store files")+
			" leaking "+plural(nameCount, "$1 file/folder name", "$1 file/folder names"))
	}

	if n := total(s.Uninspected); n > 0 {
		recap(pt, false, plural(n, "$1 file", "$1 files")+
			" not read (unsupported format)  ·  "+extBreakdown(s.Uninspected))
	}
}

// recap prints a bottom-of-report summary line, tagged [WARN] or [INFO].
func recap(pt painter, warn bool, text string) {
	if warn {
		pt.put("[WARN]  ", ansiYellow)
		pt.put(text+"\n", ansiYellow)
	} else {
		pt.put("[INFO]  ", ansiDim)
		pt.put(text+"\n", ansiDim)
	}
}

func mediaLine(pt painter, m scan.Media) {
	label, labelCode, lineCode := "[INFO]", ansiDim, ""
	if m.Revealing() {
		label, labelCode, lineCode = "[WARN]", ansiYellow, ansiYellow
	}
	pt.put("    "+label+"  ", labelCode)
	pt.put(pt.link(m.Path, m.Disk)+"\n", lineCode)

	for _, f := range [][2]string{
		{"location", m.GPS},
		{"creator", m.Creator},
		{"device", m.Camera},
		{"software", m.Software},
		{"date", m.Taken},
	} {
		if f[1] == "" {
			continue
		}
		pt.put("        "+f[0]+strings.Repeat(" ", 10-len(f[0])), ansiDim)
		pt.put(f[1] + "\n")
	}
}

func dsLine(pt painter, d scan.DSStore) {
	const show = 8
	head := d.Names
	if len(head) > show {
		head = head[:show]
	}
	pt.put("    [WARN]  ", ansiYellow)
	pt.put(pt.link(d.Path, d.Disk)+"\n", ansiYellow)
	line := "        " + plural(len(d.Names), "$1 name", "$1 names") + ": " + strings.Join(head, ", ")
	if len(d.Names) > show {
		line += fmt.Sprintf(", +%d more", len(d.Names)-show)
	}
	pt.put(line + "\n")
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
