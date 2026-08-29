// Command git-footprint reveals the personal footprint a git repository
// exposes about its contributors.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
	render(os.Stdout, fp, root, useColor(*noColor))
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
public. Lists every author/committer identity in the history and flags
personal email addresses.

REPO defaults to the current directory.
`)
}

// git

func gitRun(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
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

var personalDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true,
	"yahoo.co.jp": true, "ymail.com": true, "outlook.com": true,
	"outlook.jp": true, "hotmail.com": true, "hotmail.co.jp": true,
	"live.com": true, "live.jp": true, "icloud.com": true, "me.com": true,
	"mac.com": true, "proton.me": true, "protonmail.com": true, "pm.me": true,
	"aol.com": true, "gmx.com": true, "gmx.net": true, "zoho.com": true,
	"yandex.com": true, "fastmail.com": true, "hey.com": true,
	"tutanota.com": true, "docomo.ne.jp": true, "ezweb.ne.jp": true,
	"au.com": true, "softbank.ne.jp": true, "i.softbank.jp": true,
	"nifty.com": true, "ocn.ne.jp": true, "biglobe.ne.jp": true,
	"excite.co.jp": true, "so-net.ne.jp": true,
}

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

func domainOf(email string) string {
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		return strings.ToLower(email[i+1:])
	}
	return ""
}

func isPersonalEmail(email string) bool {
	return personalDomains[domainOf(email)]
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

func render(w io.Writer, fp footprint, repo string, color bool) {
	pt := painter{w: w, color: color}

	pt.put("git-footprint\n", ansiBold)
	pt.put("  " + repo + "\n")
	pt.put("  " + plural(fp.totalCommits, "$1 commit", "$1 commits") + " across " +
		plural(len(fp.identities), "$1 identity", "$1 identities") + "\n\n")

	personal := 0
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

		if isPersonalEmail(id.email) {
			personal++
			pt.put("    ⚠ personal address ("+domainOf(id.email)+")\n", ansiYellow)
		}
		pt.put("\n")
	}

	if personal == 0 {
		pt.put("no personal addresses in the published history\n", ansiGreen)
	} else {
		pt.put(plural(personal, "$1 personal address", "$1 personal addresses")+
			" in the published history\n", ansiYellow)
	}
}
