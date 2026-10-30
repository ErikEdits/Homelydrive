package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/erikedits/homelydrive/internal/server/core"
)

func TestDuplicates(t *testing.T) {
	e := newEnv(t, 1)
	anna := e.user("anna", false)
	foto := []byte("dasselbe Foto vom Strand")
	e.upload(anna, "home", "Handy/DCIM/IMG_1.jpg", foto)
	e.upload(anna, "home", "Handy/DCIM/IMG_1 (1).jpg", foto)
	e.upload(anna, "home", "Handy/WhatsApp/Bild.jpg", foto)
	e.upload(anna, "home", "Anderswo/IMG_1.jpg", foto) // outside the searched folder
	e.upload(anna, "home", "Handy/gleich-gross.txt", []byte("gleich lang, anderer Inhalt"))
	e.upload(anna, "home", "Handy/gleich-gross2.txt", []byte("gleich lang, anderer Inhalz"))
	e.upload(anna, "home", "Handy/leer1.txt", nil)
	e.upload(anna, "home", "Handy/leer2.txt", nil)
	// A copy put there directly on the server PC, without a checksum yet.
	home := filepath.Join(e.dir, "storage", "users", "anna")
	os.WriteFile(filepath.Join(home, "Handy", "Kopie von IMG_1.jpg"), foto, 0o644)

	var res struct{ Groups []core.DuplicateGroup }
	e.do(anna, "GET", "/api/v1/files/duplicates?space=home&path=Handy", nil, &res, 200)
	if len(res.Groups) != 1 {
		t.Fatalf("groups: %+v", res.Groups)
	}
	g := res.Groups[0]
	var paths []string
	for _, f := range g.Files {
		paths = append(paths, f.Path)
	}
	if len(g.Files) != 4 || g.Size != int64(len(foto)) || paths[0] != "Handy/DCIM/IMG_1.jpg" {
		t.Fatalf("group: size %d, %v", g.Size, paths)
	}

	// The whole drive also finds the copy outside.
	e.do(anna, "GET", "/api/v1/files/duplicates?space=home", nil, &res, 200)
	if len(res.Groups) != 1 || len(res.Groups[0].Files) != 5 {
		t.Fatalf("whole drive: %+v", res.Groups)
	}
	e.do(anna, "GET", "/api/v1/files/duplicates?space=home&path=Handy/DCIM/IMG_1.jpg", nil, nil, 400)
}
