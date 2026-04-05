package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// after returns a channel that receives after ms milliseconds.
func after(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

func TestParseConfigFile_Valid(t *testing.T) {
	f := filepath.Join(t.TempDir(), "basepaths.txt")
	if err := os.WriteFile(f, []byte(`
# comment
/mnt/nvme

/mnt/hdd
# trailing comment
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := parseConfigFile(f)
	if err != nil {
		t.Fatalf("parseConfigFile: %v", err)
	}
	want := []string{"/mnt/nvme", "/mnt/hdd"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("[%d] got %q, want %q", i, got[i], p)
		}
	}
}

func TestParseConfigFile_Empty(t *testing.T) {
	f := filepath.Join(t.TempDir(), "basepaths.txt")
	if err := os.WriteFile(f, []byte("# only comments\n\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseConfigFile(f)
	if err != nil {
		t.Fatalf("parseConfigFile: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestParseConfigFile_RelativePathRejected(t *testing.T) {
	f := filepath.Join(t.TempDir(), "basepaths.txt")
	if err := os.WriteFile(f, []byte("relative/path\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := parseConfigFile(f)
	if err == nil {
		t.Error("expected error for relative path, got nil")
	}
}

func TestParseConfigFile_Missing(t *testing.T) {
	_, err := parseConfigFile("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestWatchConfigFile_CallsReloadOnChange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "basepaths.txt")
	if err := os.WriteFile(f, []byte("/mnt/a\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	called := make(chan []string, 2)
	stop := watchConfigFile(f, 20, func(paths []string) {
		called <- paths
	})
	defer close(stop)

	// Initial load fires immediately.
	select {
	case paths := <-called:
		if len(paths) != 1 || paths[0] != "/mnt/a" {
			t.Errorf("initial reload got %v, want [/mnt/a]", paths)
		}
	case <-after(2000):
		t.Fatal("timed out waiting for initial reload")
	}

	// Update the file — watcher should call reload again.
	if err := os.WriteFile(f, []byte("/mnt/a\n/mnt/b\n"), 0o600); err != nil {
		t.Fatalf("WriteFile update: %v", err)
	}
	select {
	case paths := <-called:
		if len(paths) != 2 {
			t.Errorf("after update got %v, want [/mnt/a /mnt/b]", paths)
		}
	case <-after(2000):
		t.Fatal("timed out waiting for reload after file change")
	}
}
