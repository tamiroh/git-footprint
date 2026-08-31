// Package gitcmd wraps the git command line.
package gitcmd

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

func Run(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return string(out), err
}

func Try(repo string, args ...string) string {
	out, _ := Run(repo, args...)
	return strings.TrimSpace(out)
}

func IsRepo(path string) bool {
	_, err := Run(path, "rev-parse", "--git-dir")
	return err == nil
}

func HasCommits(repo string) bool {
	_, err := Run(repo, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

func Root(path string) (string, error) {
	out, err := Run(path, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// HeadBlobs maps each HEAD path to its blob sha, for link resolution.
func HeadBlobs(repo string) map[string]string {
	out, _ := Run(repo, "ls-tree", "-r", "-z", "HEAD")
	m := map[string]string{}
	for _, e := range strings.Split(out, "\x00") {
		tab := strings.IndexByte(e, '\t')
		if tab < 0 {
			continue
		}
		if f := strings.Fields(e[:tab]); len(f) >= 3 {
			m[e[tab+1:]] = f[2]
		}
	}
	return m
}

// CatFileBatch streams the blobs through one `git cat-file --batch`, calling fn
// for each.
func CatFileBatch(repo string, shas []string, fn func(sha string, content []byte)) error {
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, s := range shas {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return
			}
		}
		w.Flush()
	}()

	const maxBlob = 64 << 20
	br := bufio.NewReaderSize(stdout, 1<<16)
	for range shas {
		header, err := br.ReadString('\n')
		if err != nil {
			break
		}
		cols := strings.Fields(header)
		if len(cols) < 3 { // "<sha> missing"
			continue
		}
		size, err := strconv.Atoi(cols[2])
		if err != nil {
			continue
		}
		if cols[1] != "blob" || size > maxBlob { // a gitlink resolves to a commit object
			if _, err := br.Discard(size + 1); err != nil {
				break
			}
			continue
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(br, buf); err != nil {
			break
		}
		br.ReadByte()
		fn(cols[0], buf)
	}
	io.Copy(io.Discard, br) // drain so git and the writer goroutine can exit
	return cmd.Wait()
}
