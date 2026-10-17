package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnionMergeRenameTrash(t *testing.T) {
	base := t.TempDir()
	u := &Union{}
	for _, d := range []string{"a", "b"} {
		u.Roots = append(u.Roots, Root{
			Dir:      filepath.Join(base, d, "users", "x"),
			TrashDir: filepath.Join(base, d, ".trash"),
			TempDir:  filepath.Join(base, d, ".uploads"),
		})
	}
	// The same folder exists on both disks with different files.
	os.MkdirAll(filepath.Join(u.Roots[0].Dir, "Fotos"), 0o755)
	os.MkdirAll(filepath.Join(u.Roots[1].Dir, "Fotos"), 0o755)
	os.WriteFile(filepath.Join(u.Roots[0].Dir, "Fotos", "1.jpg"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(u.Roots[1].Dir, "Fotos", "2.jpg"), []byte("22"), 0o644)
	entries, err := u.ReadDir("Fotos")
	if err != nil || len(entries) != 2 {
		t.Fatalf("merged listing: %v %v", entries, err)
	}
	if err := u.Rename("Fotos", "Bilder"); err != nil {
		t.Fatal(err)
	}
	if !u.Exists("Bilder/1.jpg") || !u.Exists("Bilder/2.jpg") || u.Exists("Fotos") {
		t.Fatal("rename across disks failed")
	}
	if err := u.MoveToTrash("Bilder", "t1"); err != nil {
		t.Fatal(err)
	}
	if u.Exists("Bilder") {
		t.Fatal("still there after trash")
	}
	if err := u.RestoreFromTrash("t1", "Bilder"); err != nil {
		t.Fatal(err)
	}
	if !u.Exists("Bilder/1.jpg") || !u.Exists("Bilder/2.jpg") {
		t.Fatal("restore failed")
	}
	if _, err := u.PickRoot("Bilder", 10); err != nil {
		t.Fatal(err)
	}
}
