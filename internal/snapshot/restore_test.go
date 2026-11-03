package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreRejectsArchiveOutsideRunDirectory(t *testing.T) {
	snapshots, runs := t.TempDir(), t.TempDir()
	if err := os.WriteFile(metaPath(snapshots, 1), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(ArchivePath(snapshots, 1))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escaped.py", Mode: 0o600, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	for _, closeFile := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := closeFile(); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Restore(context.Background(), snapshots, runs, t.TempDir(), 1, 2); err == nil {
		t.Fatal("恢复接受了越界路径")
	}
	for _, path := range []string{RunCodeDir(runs, 2), filepath.Join(runs, "2", "escaped.py"), ArchivePath(snapshots, 2)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("恢复失败后留下 %s: %v", path, err)
		}
	}
}
