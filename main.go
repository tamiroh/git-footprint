// Command git-footprint reveals the personal footprint a git repository
// exposes about its contributors.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rwcarlsen/goexif/exif"
)

const version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	noColor := flag.Bool("no-color", false, "never colourise output")
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

	if !isRepo(repo) {
		fmt.Fprintf(os.Stderr, "%s is not a git repository\n", repo)
		return 2
	}
	root, err := repoRoot(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !hasCommits(root) {
		fmt.Fprintln(os.Stderr, "repository has no commits yet")
		return 2
	}

	fp, err := buildFootprint(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	images, err := scanImages(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "image scan skipped:", err)
	}

	render(os.Stdout, fp, images, root, useColor(*noColor))
	return 0
}

func useColor(noColor bool) bool {
	if noColor {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func usage() {
	fmt.Fprint(os.Stderr, `git-footprint [--no-color] [--version] [REPO]

Check what your git history reveals about you before you make a repository
public. Lists every author/committer identity in the history and the EXIF
metadata (location, creator, camera) of any image each of them committed.

REPO defaults to the current directory.
`)
}

// git

func gitBytes(repo string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
}

func gitRun(repo string, args ...string) (string, error) {
	out, err := gitBytes(repo, args...)
	return string(out), err
}

func gitTry(repo string, args ...string) string {
	out, _ := gitRun(repo, args...)
	return strings.TrimSpace(out)
}

func isRepo(path string) bool {
	_, err := gitRun(path, "rev-parse", "--git-dir")
	return err == nil
}

func hasCommits(repo string) bool {
	_, err := gitRun(repo, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

func repoRoot(path string) (string, error) {
	out, err := gitRun(path, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// identities

const fieldSep = "\x1f" // ASCII Unit Separator, used to split git-log --format fields

type identity struct {
	name             string
	email            string
	authorCommits    int
	committerCommits int
	firstDate        string
	lastDate         string
	isYou            bool
}

type footprint struct {
	totalCommits int
	identities   []identity
}

func collectIdentities(repo string) ([]identity, error) {
	fields := []string{"%an", "%ae", "%ad", "%cn", "%ce", "%cd"}
	// --branches --tags --remotes, not --all: keep local-only refs/stash and
	// refs/notes out of the footprint.
	out, err := gitRun(repo, "log", "HEAD", "--branches", "--tags", "--remotes",
		"--no-color", "--date=short", "--format="+strings.Join(fields, fieldSep))
	if err != nil {
		return nil, err
	}

	type key struct{ name, email string }
	byKey := map[key]*identity{}
	var order []key

	note := func(name, email, date string, author bool) {
		email = strings.ToLower(email)
		k := key{name, email}
		id := byKey[k]
		if id == nil {
			id = &identity{name: name, email: email, firstDate: date, lastDate: date}
			byKey[k] = id
			order = append(order, k)
		}
		if author {
			id.authorCommits++
		} else {
			id.committerCommits++
		}
		if len(date) == 10 {
			if id.firstDate == "" || date < id.firstDate {
				id.firstDate = date
			}
			if date > id.lastDate {
				id.lastDate = date
			}
		}
	}

	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, fieldSep)
		if len(f) < 6 {
			continue
		}
		note(f[0], f[1], f[2], true)
		note(f[3], f[4], f[5], false)
	}

	ids := make([]identity, 0, len(order))
	for _, k := range order {
		ids = append(ids, *byKey[k])
	}
	return ids, nil
}

func buildFootprint(repo string) (footprint, error) {
	youEmail := strings.ToLower(gitTry(repo, "config", "user.email"))
	youName := gitTry(repo, "config", "user.name")

	ids, err := collectIdentities(repo)
	if err != nil {
		return footprint{}, err
	}

	switch {
	case youEmail != "":
		for i := range ids {
			ids[i].isYou = ids[i].email == youEmail
		}
	case youName != "":
		for i := range ids {
			ids[i].isYou = ids[i].name == youName
		}
	}

	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i].isYou != ids[j].isYou {
			return ids[i].isYou
		}
		return ids[i].authorCommits+ids[i].committerCommits >
			ids[j].authorCommits+ids[j].committerCommits
	})

	commits := 0
	for _, id := range ids {
		commits += id.authorCommits
	}
	return footprint{totalCommits: commits, identities: ids}, nil
}

// images

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".tif": true, ".tiff": true,
}

type imageMeta struct {
	path    string
	byName  string // author of the commit that introduced this blob
	byEmail string
	gps     string // "lat, long"
	creator string // Artist or Copyright
	camera  string // "Make Model"
	taken   string // "2006-01-02 15:04:05"
}

func (m imageMeta) revealing() bool { return m.gps != "" || m.creator != "" }

func (m imageMeta) empty() bool {
	return m.gps == "" && m.creator == "" && m.camera == "" && m.taken == ""
}

// scanImages walks every blob ever added or changed, and for image blobs pulls
// EXIF out, tagging each with the author of the commit that introduced it.
func scanImages(repo string) ([]imageMeta, error) {
	const hdr = "\x1e"
	out, err := gitRun(repo, "log", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--date=short",
		"--format="+hdr+"%an"+fieldSep+"%ae"+fieldSep+"%ad", "--raw")
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var name, email string
	var found []imageMeta
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
			cols := strings.Fields(meta)
			if len(cols) < 5 {
				continue
			}
			sha := cols[3]
			if seen[sha] || !imageExts[strings.ToLower(filepath.Ext(path))] {
				continue
			}
			seen[sha] = true

			blob, err := gitBytes(repo, "cat-file", "-p", sha)
			if err != nil || len(blob) > 64<<20 {
				continue
			}
			if m := readEXIF(path, blob); !m.empty() {
				m.byName, m.byEmail = name, email
				found = append(found, m)
			}
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].revealing() != found[j].revealing() {
			return found[i].revealing()
		}
		return found[i].path < found[j].path
	})
	return found, nil
}

func readEXIF(path string, blob []byte) (m imageMeta) {
	m.path = path
	defer func() { _ = recover() }() // goexif can panic on hostile input

	x, err := exif.Decode(bytes.NewReader(blob))
	if err != nil {
		return m
	}

	if lat, long, err := x.LatLong(); err == nil && (lat != 0 || long != 0) {
		m.gps = fmt.Sprintf("%.5f, %.5f", lat, long)
	}
	for _, name := range []exif.FieldName{exif.Artist, exif.Copyright} {
		if v := exifStr(x, name); v != "" {
			m.creator = v
			break
		}
	}
	m.camera = cameraName(exifStr(x, exif.Make), exifStr(x, exif.Model))
	if t, err := x.DateTime(); err == nil {
		m.taken = t.Format("2006-01-02 15:04:05")
	}
	return m
}

func cameraName(mk, model string) string {
	mk, model = strings.TrimSpace(mk), strings.TrimSpace(model)
	switch {
	case mk == "":
		return model
	case model == "":
		return mk
	}
	if brand := strings.Fields(mk); len(brand) > 0 &&
		strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand[0])) {
		return model // model already carries the maker, e.g. "NIKON D2H"
	}
	return mk + " " + model
}

func exifStr(x *exif.Exif, name exif.FieldName) string {
	tag, err := x.Get(name)
	if err != nil {
		return ""
	}
	s, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return strings.TrimRight(s, "\x00 ")
}

// report

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

type painter struct {
	w     io.Writer
	color bool
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

func commitCount(id identity) string {
	switch {
	case id.authorCommits == 0:
		return plural(id.committerCommits, "$1 commit", "$1 commits") + " (as committer only)"
	case id.committerCommits == 0:
		return plural(id.authorCommits, "$1 commit", "$1 commits") + " (as author only)"
	case id.authorCommits != id.committerCommits:
		return plural(id.authorCommits, "$1 commit", "$1 commits") +
			fmt.Sprintf(" (%d as committer)", id.committerCommits)
	default:
		return plural(id.authorCommits, "$1 commit", "$1 commits")
	}
}

func dateRange(id identity) string {
	switch {
	case len(id.firstDate) == 10 && id.firstDate != id.lastDate:
		return id.firstDate + " -> " + id.lastDate
	case len(id.firstDate) == 10:
		return id.firstDate
	default:
		return ""
	}
}

func render(w io.Writer, fp footprint, images []imageMeta, repo string, color bool) {
	pt := painter{w: w, color: color}

	pt.put("git-footprint\n", ansiBold)
	pt.put("  " + repo + "\n")
	pt.put("  " + plural(fp.totalCommits, "$1 commit", "$1 commits") + " across " +
		plural(len(fp.identities), "$1 identity", "$1 identities") + "\n\n")

	byWho := map[[2]string][]imageMeta{}
	for _, m := range images {
		k := [2]string{m.byName, m.byEmail}
		byWho[k] = append(byWho[k], m)
	}

	revealing := 0
	for _, id := range fp.identities {
		youColor := ""
		if id.isYou {
			youColor = ansiGreen
		}
		pt.put("● ", ansiBold, youColor)
		if id.isYou {
			pt.put("you  ", ansiBold, ansiGreen)
		}
		pt.put(id.name+" <"+id.email+">\n", ansiBold)

		line := "    " + commitCount(id)
		if dr := dateRange(id); dr != "" {
			line += "  ·  " + dr
		}
		pt.put(line + "\n")

		k := [2]string{id.name, id.email}
		for _, m := range byWho[k] {
			if m.revealing() {
				revealing++
			}
			imageLine(pt, m)
		}
		delete(byWho, k)

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
			if m.revealing() {
				revealing++
			}
			imageLine(pt, m)
		}
	}

	if len(images) > 0 {
		s := plural(len(images), "$1 image carries EXIF metadata", "$1 images carry EXIF metadata")
		if revealing > 0 {
			pt.put(s+fmt.Sprintf(" (%d reveal a location or creator)\n", revealing), ansiYellow)
		} else {
			pt.put(s + "\n")
		}
	}
}

func imageLine(pt painter, m imageMeta) {
	marker, code := "● ", ""
	if m.revealing() {
		marker, code = "⚠ ", ansiYellow
	}
	var parts []string
	if m.gps != "" {
		parts = append(parts, "location "+m.gps)
	}
	if m.creator != "" {
		parts = append(parts, "creator "+m.creator)
	}
	if m.camera != "" {
		parts = append(parts, m.camera)
	}
	if m.taken != "" {
		parts = append(parts, m.taken)
	}
	pt.put("    "+marker+m.path+"  ·  "+strings.Join(parts, "  ·  ")+"\n", code)
}
