// Command git-footprint reveals the personal footprint a git repository
// exposes about its contributors.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tamiroh/git-footprint/internal/dsstore"
	"github.com/tamiroh/git-footprint/internal/exif"
	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/identity"
)

const version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	noColor := flag.Bool("no-color", false, "never colourise output")
	forceColor := flag.Bool("color", false, "colourise output even when not a terminal")
	noPager := flag.Bool("no-pager", false, "do not page output through $PAGER")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("git-footprint", version)
		return 0
	}

	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "git was not found on PATH")
		return 2
	}

	repoArg := "."
	if flag.NArg() > 0 {
		repoArg = flag.Arg(0)
	}
	repo, err := filepath.Abs(repoArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if !gitcmd.IsRepo(repo) {
		fmt.Fprintf(os.Stderr, "%s is not a git repository\n", repo)
		return 2
	}
	root, err := gitcmd.Root(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !gitcmd.HasCommits(root) {
		fmt.Fprintln(os.Stderr, "repository has no commits yet")
		return 2
	}

	fp, err := identity.Build(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	scan, err := scanBlobs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blob scan incomplete:", err)
	}

	tty := isTerminal(os.Stdout)
	color := (tty || *forceColor) && !*noColor

	out, closePager := startPager(tty && !*noPager)
	defer closePager()
	render(out, fp, scan, root, color)
	return 0
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func startPager(enabled bool) (io.Writer, func()) {
	if !enabled {
		return os.Stdout, func() {}
	}
	name := os.Getenv("PAGER")
	if name == "" {
		name = "less"
	}
	if name == "cat" {
		return os.Stdout, func() {}
	}

	cmd := exec.Command("sh", "-c", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if os.Getenv("LESS") == "" {
		cmd.Env = append(cmd.Env, "LESS=FR")
	}
	w, err := cmd.StdinPipe()
	if err != nil {
		return os.Stdout, func() {}
	}
	if err := cmd.Start(); err != nil {
		return os.Stdout, func() {}
	}
	return w, func() {
		w.Close()
		cmd.Wait()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `git-footprint [--no-color] [--color] [--no-pager] [--version] [REPO]

Check what your git history reveals about you before you make a repository
public. Per contributor: every identity in the history, the EXIF metadata
(location, creator, camera) of any image they committed, and the file names a
committed .DS_Store leaks.

REPO defaults to the current directory.
`)
}

// blobs

const fieldSep = "\x1f" // ASCII Unit Separator

type imageMeta struct {
	exif.Data
	path    string
	disk    string // abs path a hyperlink should open (working tree, or a temp extract)
	byName  string // author of the commit that introduced this blob
	byEmail string
}

type blobScan struct {
	images      []imageMeta
	dsStores    []dsStoreLeak
	uninspected map[string]int // extension -> count of binary blobs nothing was read from
}

type blobRef struct {
	path, byName, byEmail string
}

type dsStoreLeak struct {
	path            string
	disk            string
	byName, byEmail string
	names           []string
}

// linkTarget is the absolute path a finding's hyperlink should open. When the
// working tree holds exactly the bytes the finding came from, that file;
// otherwise (for extractable types) a copy of the historical bytes under the
// temp dir. "" means no link.
func linkTarget(root, path, sha string, content []byte, extract bool, head map[string]string) string {
	if head[path] == sha {
		return filepath.Join(root, path)
	}
	if !extract {
		return ""
	}
	dir := filepath.Join(os.TempDir(), "git-footprint")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	tmp := filepath.Join(dir, sha[:12]+"-"+filepath.Base(path))
	if _, err := os.Stat(tmp); err != nil {
		if os.WriteFile(tmp, content, 0o644) != nil {
			return ""
		}
	}
	return tmp
}

// scanBlobs reads every blob ever added or changed, pulls EXIF from images, and
// tallies the binary blobs it had nothing to say about.
func scanBlobs(repo string) (blobScan, error) {
	const hdr = "\x1e"
	out, err := gitcmd.Run(repo, "-c", "core.quotePath=false",
		"log", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--format="+hdr+"%an"+fieldSep+"%ae", "--raw")
	if err != nil {
		return blobScan{}, err
	}
	head := gitcmd.HeadBlobs(repo)

	bySha := map[string]blobRef{}
	var shas []string
	var name, email string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, hdr):
			if f := strings.Split(line[len(hdr):], fieldSep); len(f) >= 2 {
				name, email = f[0], strings.ToLower(f[1])
			}
		case strings.HasPrefix(line, ":"):
			meta, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			if strings.HasPrefix(path, `"`) {
				if uq, e := strconv.Unquote(path); e == nil {
					path = uq
				}
			}
			cols := strings.Fields(meta)
			if len(cols) < 5 {
				continue
			}
			sha := cols[3]
			if _, dup := bySha[sha]; dup || strings.Trim(sha, "0") == "" {
				continue
			}
			bySha[sha] = blobRef{path, name, email}
			shas = append(shas, sha)
		}
	}

	scan := blobScan{uninspected: map[string]int{}}
	err = gitcmd.CatFileBatch(repo, shas, func(sha string, content []byte) {
		if !looksBinary(content) {
			return
		}
		ref := bySha[sha]

		if filepath.Base(ref.path) == ".DS_Store" {
			if names := dsstore.Names(content); len(names) > 0 {
				scan.dsStores = append(scan.dsStores, dsStoreLeak{
					path: ref.path, disk: linkTarget(repo, ref.path, sha, content, false, head),
					byName: ref.byName, byEmail: ref.byEmail, names: names,
				})
			}
			return
		}

		ext := strings.ToLower(filepath.Ext(ref.path))
		if exif.IsImage(ref.path) {
			// a supported image type: inspected, whether or not it had EXIF
			if d := exif.Read(content); !d.Empty() {
				m := imageMeta{Data: d, path: ref.path}
				m.byName, m.byEmail = ref.byName, ref.byEmail
				m.disk = linkTarget(repo, ref.path, sha, content, true, head)
				scan.images = append(scan.images, m)
			}
			return
		}
		scan.uninspected[ext]++ // a binary format git-footprint does not read
	})

	scan.images = dedupeImages(scan.images)
	sort.SliceStable(scan.images, func(i, j int) bool {
		if scan.images[i].Revealing() != scan.images[j].Revealing() {
			return scan.images[i].Revealing()
		}
		return scan.images[i].path < scan.images[j].path
	})
	sort.SliceStable(scan.dsStores, func(i, j int) bool {
		return scan.dsStores[i].path < scan.dsStores[j].path
	})
	return scan, err
}

func dedupeImages(in []imageMeta) []imageMeta {
	type key struct{ path, byName, byEmail, gps, creator, camera, taken string }
	seen := map[key]bool{}
	var out []imageMeta
	for _, m := range in {
		k := key{m.path, m.byName, m.byEmail, m.GPS, m.Creator, m.Camera, m.Taken}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

func looksBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// report

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

func render(w io.Writer, fp identity.Footprint, scan blobScan, repo string, color bool) {
	pt := painter{w: w, color: color}
	images := scan.images

	headerBox(pt,
		"git-footprint",
		repo,
		plural(fp.TotalCommits, "$1 commit", "$1 commits")+" across "+
			plural(len(fp.Identities), "$1 identity", "$1 identities"),
	)

	byWho := map[[2]string][]imageMeta{}
	for _, m := range images {
		k := [2]string{m.byName, m.byEmail}
		byWho[k] = append(byWho[k], m)
	}
	dsByWho := map[[2]string][]dsStoreLeak{}
	for _, d := range scan.dsStores {
		k := [2]string{d.byName, d.byEmail}
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
			imageLine(pt, m)
		}
		for _, d := range dsByWho[k] {
			dsLine(pt, d)
		}
		delete(byWho, k)
		delete(dsByWho, k)

		pt.put("\n")
	}

	var orphan []imageMeta
	for _, ms := range byWho {
		orphan = append(orphan, ms...)
	}
	if len(orphan) > 0 {
		sort.SliceStable(orphan, func(i, j int) bool { return orphan[i].path < orphan[j].path })
		pt.put("\nimages not tied to a listed identity\n", ansiBold)
		for _, m := range orphan {
			if m.Revealing() {
				revealing++
			}
			imageLine(pt, m)
		}
	}

	var dsOrphan []dsStoreLeak
	for _, ds := range dsByWho {
		dsOrphan = append(dsOrphan, ds...)
	}
	if len(dsOrphan) > 0 {
		sort.SliceStable(dsOrphan, func(i, j int) bool { return dsOrphan[i].path < dsOrphan[j].path })
		pt.put("\n.DS_Store files not tied to a listed identity\n", ansiBold)
		for _, d := range dsOrphan {
			dsLine(pt, d)
		}
	}

	if len(images) > 0 {
		s := plural(len(images), "$1 image carries EXIF metadata", "$1 images carry EXIF metadata")
		if revealing > 0 {
			pt.put(s+" ("+plural(revealing, "$1 reveals", "$1 reveal")+
				" a location or creator)\n", ansiYellow)
		} else {
			pt.put(s + "\n")
		}
	}

	if n := len(scan.dsStores); n > 0 {
		nameCount := 0
		for _, d := range scan.dsStores {
			nameCount += len(d.names)
		}
		pt.put(plural(n, "$1 committed .DS_Store", "$1 committed .DS_Store files")+
			" leaking "+plural(nameCount, "$1 file/folder name", "$1 file/folder names")+"\n",
			ansiYellow)
	}

	if n := total(scan.uninspected); n > 0 {
		pt.put("not read (unsupported format): "+
			plural(n, "$1 binary file", "$1 binary files")+
			"  ·  "+extBreakdown(scan.uninspected)+"\n", ansiDim)
	}
}

func dsLine(pt painter, d dsStoreLeak) {
	const show = 8
	head := d.names
	if len(head) > show {
		head = head[:show]
	}
	s := "    ⚠ " + pt.link(d.path, d.disk) + "  ·  " +
		plural(len(d.names), "$1 name", "$1 names") + ": " + strings.Join(head, ", ")
	if len(d.names) > show {
		s += fmt.Sprintf(", +%d more", len(d.names)-show)
	}
	pt.put(s+"\n", ansiYellow)
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

func imageLine(pt painter, m imageMeta) {
	marker, code := "● ", ""
	if m.Revealing() {
		marker, code = "⚠ ", ansiYellow
	}
	var parts []string
	if m.GPS != "" {
		parts = append(parts, "location "+m.GPS)
	}
	if m.Creator != "" {
		parts = append(parts, "creator "+m.Creator)
	}
	if m.Camera != "" {
		parts = append(parts, m.Camera)
	}
	if m.Taken != "" {
		parts = append(parts, m.Taken)
	}
	pt.put("    "+marker+pt.link(m.path, m.disk)+"  ·  "+strings.Join(parts, "  ·  ")+"\n", code)
}
