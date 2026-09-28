package storage

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestSearchIsBoundedAndStaysInside(t *testing.T) {
	s, root, staging := fixture(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "Raport-outside.txt"), "x")
	os.MkdirAll(filepath.Join(root, "a", "b"), 0700)
	write(t, filepath.Join(root, "Raport 2024.pdf"), "x")
	write(t, filepath.Join(root, "a", "raport.txt"), "x")
	write(t, filepath.Join(root, "a", "b", "RAPORTY"), "x")
	write(t, filepath.Join(root, "a", "other.txt"), "x")
	write(t, filepath.Join(staging, "raport-hidden.part"), "x")
	os.Symlink(outside, filepath.Join(root, "link-to-outside"))
	os.Symlink(filepath.Join(outside, "Raport-outside.txt"), filepath.Join(root, "raport-link"))

	hits, truncated, err := s.Search(context.Background(), ".", "raport", SearchLimits{MaxScanned: 1000, MaxResults: 100, MaxDepth: 10})
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	sort.Strings(paths)
	want := []string{"Raport 2024.pdf", "a/b/RAPORTY", "a/raport.txt"}
	if len(paths) != len(want) {
		t.Fatal(paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatal(paths)
		}
	}
	if hits[0].Modified.IsZero() {
		t.Fatal("modification time missing")
	}
	// Scoped to a folder.
	if hits, _, _ = s.Search(context.Background(), "a/b", "rap", SearchLimits{MaxScanned: 100, MaxResults: 10, MaxDepth: 10}); len(hits) != 1 || hits[0].Path != "a/b/RAPORTY" {
		t.Fatal(hits)
	}
	// Limits report truncation instead of failing.
	if _, truncated, err = s.Search(context.Background(), ".", "raport", SearchLimits{MaxScanned: 1000, MaxResults: 1, MaxDepth: 10}); err != nil || !truncated {
		t.Fatal(err, truncated)
	}
	if _, truncated, _ = s.Search(context.Background(), ".", "raport", SearchLimits{MaxScanned: 2, MaxResults: 10, MaxDepth: 10}); !truncated {
		t.Fatal("scan limit not reported")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, truncated, _ = s.Search(ctx, ".", "raport", SearchLimits{MaxScanned: 100, MaxResults: 10, MaxDepth: 10}); !truncated {
		t.Fatal("deadline not reported")
	}
	for _, bad := range []string{"../", ".filedeck", "link-to-outside"} {
		if _, _, err = s.Search(context.Background(), bad, "raport", SearchLimits{MaxScanned: 100, MaxResults: 10, MaxDepth: 10}); err == nil {
			t.Error("searched", bad)
		}
	}
}
