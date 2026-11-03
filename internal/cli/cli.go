package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/tamiroh/git-footprint/internal/engine"
	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/report"
	"github.com/tamiroh/git-footprint/internal/rule"
	"github.com/tamiroh/git-footprint/internal/rules/archive"
	"github.com/tamiroh/git-footprint/internal/rules/dsstore"
	"github.com/tamiroh/git-footprint/internal/rules/font"
	"github.com/tamiroh/git-footprint/internal/rules/image"
	"github.com/tamiroh/git-footprint/internal/rules/office"
	"github.com/tamiroh/git-footprint/internal/rules/pdf"
	"github.com/tamiroh/git-footprint/internal/rules/video"
	"github.com/tamiroh/git-footprint/internal/source"
)

// Run executes the command and returns its exit code.
func Run(args []string) int {
	flags := flag.NewFlagSet("git-footprint", flag.ContinueOnError)
	var allowed identity.AllowList
	flags.Var(&allowed, "allow-identity", "allow Name <email>; repeat for each identity (warn on all others)")
	noColor := flags.Bool("no-color", false, "never colourise output")
	forceColor := flags.Bool("color", false, "colourise output even when not a terminal")
	noPager := flags.Bool("no-pager", false, "do not page output through $PAGER")
	failOn := flags.String("fail-on", "none", "exit 1 if findings reach this level: none, info, warn")
	showVersion := flags.Bool("version", false, "print version and exit")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: git footprint [options] [REPO]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		version := "(devel)"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
		fmt.Println("git-footprint", version)
		return 0
	}

	switch strings.ToLower(*failOn) {
	case "none", "info", "warn":
	default:
		fmt.Fprintf(os.Stderr, "invalid --fail-on %q: want none, info or warn\n", *failOn)
		return 2
	}

	repoArg := "."
	if flags.NArg() > 0 {
		repoArg = flags.Arg(0)
	}
	repo, err := filepath.Abs(repoArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	src, err := source.Open(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fp, err := src.Footprint()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	tty := isTerminal(os.Stdout)
	color := (tty || *forceColor) && !*noColor

	result, scanErr := engine.New(src, []rule.Rule{
		image.New(),
		video.New(),
		pdf.New(),
		office.New(),
		font.New(),
		dsstore.New(),
		archive.New(),
	}, color).Run()
	result.Findings = append(result.Findings, allowed.Findings(fp)...)

	out, closePager := startPager(tty && !*noPager)
	report.Render(out, fp, result, color)
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
		_ = w.Close()
		_ = cmd.Wait()
	}
}
