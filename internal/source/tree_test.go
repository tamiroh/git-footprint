package source

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/tamiroh/git-footprint/internal/rule"
)

func TestTreeSourceBlobs(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt")
	write("sub/b.txt")
	write(".git/config")
	write(".git/objects/ab/cdef")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("a.txt", filepath.Join(root, "link.txt")); err != nil {
			t.Fatal(err)
		}
	}

	var paths []string
	if err := (&treeSource{root: root}).Blobs(func(b rule.Blob) {
		paths = append(paths, b.Path)
	}); err != nil {
		t.Fatalf("Blobs: %v", err)
	}

	sort.Strings(paths)
	want := []string{"a.txt", "sub/b.txt"} // .git skipped, symlink skipped, nesting kept, slashes forward
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}
