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

	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/report"
	"github.com/tamiroh/git-footprint/internal/scan"
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

	result, err := scan.Blobs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blob scan incomplete:", err)
	}

	tty := isTerminal(os.Stdout)
	color := (tty || *forceColor) && !*noColor

	out, closePager := startPager(tty && !*noPager)
	defer closePager()
	report.Render(out, fp, result, root, color)
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
public. Per contributor: every identity in the history, the embedded metadata
(location, creator, camera, software, capture time) of any image, video or PDF
they committed, and the file names a committed .DS_Store leaks.

REPO defaults to the current directory.
`)
}
