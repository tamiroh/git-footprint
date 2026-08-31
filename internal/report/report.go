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
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

type painter struct {
	w     io.Writer
	color bool
}

// put scrubs control sequences from caller text — it comes from the scanned,
// possibly hostile repository.
func (pt painter) put(text string, codes ...string) { pt.write(sane(text), codes...) }

func (pt painter) putLink(text, target string, codes ...string) {
	text = sane(text)
	if pt.color && target != "" {
		uri := (&url.URL{Scheme: "file", Path: target}).String()
		text = "\x1b]8;;" + uri + "\x1b\\" + text + "\x1b]8;;\x1b\\"
	}
	pt.write(text+"\n", codes...)
}

func (pt painter) write(text string, codes ...string) {
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

func ctrl(r rune) bool { return (r < 0x20 && r != '\n') || (r >= 0x7f && r <= 0x9f) }

func sane(s string) string {
	if strings.IndexFunc(s, ctrl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if ctrl(r) {
			return '�'
		}
		return r
	}, s)
}

func plural(n int, one, many string) string {
	form := many
	if n == 1 {
		form = one
	}
	return strings.ReplaceAll(form, "$1", fmt.Sprint(n))
}

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

// rank orders detectors; unknown ones sort last.
func rank(detector string) int {
	if r, ok := map[string]int{
		"image-metadata": 0, "video-metadata": 1, "pdf-metadata": 2,
		"office-metadata": 3, "ds-store": 4, "archive": 5,
	}[detector]; ok {
		return r
	}
	return 99
}

func checkLabel(name string) string { // "image-location" -> "location"
	return name[strings.LastIndexByte(name, '-')+1:]
}

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

	var mine, rest []identity.Identity
	for _, id := range fp.Identities {
		if id.Self != identity.NotSelf {
			mine = append(mine, id)
		} else {
			rest = append(rest, id)
		}
	}
	sectionHead(pt, "your identities")
	if len(mine) == 0 {
		none(pt)
	}
	for _, id := range mine {
		identityBlock(pt, id, byWho)
	}

	sectionHead(pt, "other contributors")
	if len(rest) == 0 {
		none(pt)
	}
	for _, id := range rest {
		identityBlock(pt, id, byWho)
	}

	var orphans []rule.Finding
	for _, fs := range byWho {
		orphans = append(orphans, fs...)
	}
	sectionHead(pt, "not tied to a listed identity")
	if len(orphans) == 0 {
		none(pt)
	} else {
		for _, f := range sortFindings(orphans) {
			findingBlock(pt, f)
		}
		pt.put("\n")
	}

	sectionHead(pt, "summary")
	summary(pt, res)
}

func none(pt painter) { pt.put("(none)\n\n", ansiDim) }

func sectionHead(pt painter, title string) {
	u := strings.ToUpper(title)
	pt.put(u+"\n", ansiBold)
	pt.put(strings.Repeat("─", termWidth(u))+"\n\n", ansiDim)
}

func identityBlock(pt painter, id identity.Identity, byWho map[[2]string][]rule.Finding) {
	nameCode, code := ansiBold, ""
	if id.Bot {
		nameCode, code = ansiDim, ansiDim
	}
	pt.put("● "+id.Name+" <"+id.Email+">", nameCode)
	switch id.Self {
	case identity.IsSelf:
		pt.put("  (you)", ansiBold, ansiGreen)
	case identity.MaybeSelf:
		pt.put("  (maybe you)", ansiDim)
	}
	pt.put("\n")

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

func sortFindings(in []rule.Finding) []rule.Finding {
	out := append([]rule.Finding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank(out[i].Detector), rank(out[j].Detector); a != b {
			return a < b
		}
		if a, b := out[i].Level(), out[j].Level(); a != b {
			return a > b // Warn before Info
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func ofDetector(in []rule.Finding, names ...string) []rule.Finding {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []rule.Finding
	for _, f := range in {
		if want[f.Detector] {
			out = append(out, f)
		}
	}
	return out
}

func findingBlock(pt painter, f rule.Finding) {
	label, labelCode, lineCode := "[INFO]", ansiDim, ""
	if f.Level() == rule.Warn {
		label, labelCode, lineCode = "[WARN]", ansiYellow, ansiYellow
	}
	pt.put("    "+label+"  ", labelCode)
	pt.putLink(f.Path, f.Link, lineCode)

	for _, c := range f.Checks {
		name := checkLabel(c.Name)
		gap := 13 - len(name)
		if gap < 1 {
			gap = 1
		}
		pt.put("        "+name+strings.Repeat(" ", gap), ansiDim)
		pt.put(c.Value + "\n")
	}
}

type mark int

const (
	markOK mark = iota // nothing to flag; no tag
	markInfo
	markWarn
)

func recap(pt painter, m mark, text string) {
	gutter, code := "        ", ansiGreen
	switch m {
	case markInfo:
		gutter, code = "[INFO]  ", ansiDim
	case markWarn:
		gutter, code = "[WARN]  ", ansiYellow
	}
	pt.put(gutter, code)
	pt.put(text+"\n", code)
}

func summary(pt painter, res rule.Result) {
	meta := ofDetector(res.Findings, "image-metadata", "video-metadata", "pdf-metadata", "office-metadata")
	if len(meta) == 0 {
		recap(pt, markOK, "no committed file carries embedded metadata")
	} else {
		revealing := 0
		for _, f := range meta {
			if f.Level() == rule.Warn {
				revealing++
			}
		}
		line := plural(len(meta), "$1 file carries embedded metadata", "$1 files carry embedded metadata")
		if revealing > 0 {
			line += " (" + plural(revealing, "$1 reveals", "$1 reveal") + " a location or creator)"
			recap(pt, markWarn, line)
		} else {
			recap(pt, markInfo, line)
		}
	}

	if ds := ofDetector(res.Findings, "ds-store"); len(ds) == 0 {
		recap(pt, markOK, "no committed .DS_Store")
	} else {
		names := 0
		for _, f := range ds {
			names += f.Count
		}
		recap(pt, markWarn, plural(len(ds), "$1 committed .DS_Store", "$1 committed .DS_Store files")+
			" leaking "+plural(names, "$1 file/folder name", "$1 file/folder names"))
	}

	if arc := ofDetector(res.Findings, "archive"); len(arc) == 0 {
		recap(pt, markOK, "no committed archive names its owner")
	} else {
		recap(pt, markWarn, plural(len(arc),
			"$1 committed archive names its owner", "$1 committed archives name their owner"))
	}

	if n := total(res.Unclaimed); n == 0 {
		recap(pt, markOK, "every committed file was read")
	} else {
		recap(pt, markInfo, plural(n, "$1 file", "$1 files")+
			" not read (unsupported format)  ·  "+extBreakdown(res.Unclaimed))
	}
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// extBreakdown renders the unread-file tally. Non-extension keys (content-hashed
// names produce thousands) and the tail past 12 fold into "other".
func extBreakdown(m map[string]int) string {
	type kv struct {
		ext string
		n   int
	}
	var kvs []kv
	other := 0
	for e, n := range m {
		if looksExt(e) {
			kvs = append(kvs, kv{e, n})
		} else {
			other += n
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].ext < kvs[j].ext
	})

	const show = 12
	var parts []string
	for i, k := range kvs {
		if i == show {
			for _, rest := range kvs[i:] {
				other += rest.n
			}
			break
		}
		ext := k.ext
		if ext == "" {
			ext = "(no ext)"
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", ext, k.n))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("other ×%d", other))
	}
	return strings.Join(parts, "  ·  ")
}

func looksExt(e string) bool {
	if e == "" {
		return true
	}
	if e[0] != '.' || len(e) > 10 {
		return false
	}
	for _, r := range e[1:] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
