// Command git-footprint reports what a repository's history reveals about its
// contributors.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/report"
	"github.com/tamiroh/git-footprint/internal/rule"
	"github.com/tamiroh/git-footprint/internal/rules/archive"
	"github.com/tamiroh/git-footprint/internal/rules/dsstore"
	"github.com/tamiroh/git-footprint/internal/rules/image"
	"github.com/tamiroh/git-footprint/internal/rules/office"
	"github.com/tamiroh/git-footprint/internal/rules/pdf"
	"github.com/tamiroh/git-footprint/internal/rules/video"
)

const version = "0.1.1"

func main() {
	os.Exit(run())
}

func run() int {
	noColor := flag.Bool("no-color", false, "never colourise output")
	forceColor := flag.Bool("color", false, "colourise output even when not a terminal")
	noPager := flag.Bool("no-pager", false, "do not page output through $PAGER")
	failOn := flag.String("fail-on", "none", "exit 1 if findings reach this level: none, info, warn")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("git-footprint", version)
		return 0
	}

	switch strings.ToLower(*failOn) {
	case "none", "info", "warn":
	default:
		fmt.Fprintf(os.Stderr, "invalid --fail-on %q: want none, info or warn\n", *failOn)
		return 2
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

	tty := isTerminal(os.Stdout)
	color := (tty || *forceColor) && !*noColor

	engine := rule.NewEngine(root, []rule.Rule{
		image.New(),
		video.New(),
		pdf.New(),
		office.New(),
		dsstore.New(),
		archive.New(),
	}, color)
	result, scanErr := engine.Run()

	out, closePager := startPager(tty && !*noPager)
	report.Render(out, fp, result, root, color)
	closePager()

	if scanErr != nil {
		// after the pager, so it's the last thing on screen
		fmt.Fprintln(os.Stderr, "blob scan incomplete:", scanErr)
		return 2 // an incomplete scan can't answer --fail-on
	}
	found, level := result.Worst()
	switch strings.ToLower(*failOn) {
	case "warn":
		if level >= rule.Warn {
			return 1
		}
	case "info":
		if found {
			return 1
		}
	}
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
	fmt.Fprint(os.Stderr, `git-footprint [--no-color] [--color] [--no-pager] [--fail-on LEVEL] [--version] [REPO]

Check what your git history reveals about you before you make a repository
public. Per contributor: every identity in the history, the embedded metadata
(location, creator, camera, software, creation date) of any image, video, PDF or
Office document they committed, and the file names a committed .DS_Store leaks.

REPO defaults to the current directory.

--fail-on LEVEL exits 1 when findings reach LEVEL (none, info, warn); "warn"
covers any finding that reveals a location or creator, or a committed
.DS_Store. Setup errors always exit 2.
`)
}
