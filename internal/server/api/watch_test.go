package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/erikedits/homelydrive/internal/server/core"
)

// A folder with subfolders must stay renamable, movable and deletable while
// the server watches it. Windows refuses to move a folder while anything
// below it is open, and file watches used to keep every subfolder open.
func TestWatchedFolderTree(t *testing.T) {
	e := newEnv(t, 1)
	anna := e.user("anna", false)
	for _, p := range []string{"Handy/DCIM/Camera/bild.jpg", "Handy/Download/doc.pdf", "Handy/WhatsApp/Media/Images/a.jpg"} {
		e.upload(anna, "home", p, []byte(p))
	}
	e.do(anna, "POST", "/api/v1/files/mkdir", map[string]string{"path": "Archiv"}, nil, 200)
	time.Sleep(time.Second)
	e.core.ScanAll() // watches every folder

	e.do(anna, "POST", "/api/v1/files/rename", map[string]string{"path": "Handy", "new_name": "Samsung SM-A556B"}, nil, 200)
	e.do(anna, "POST", "/api/v1/files/move", map[string]any{"paths": []string{"Samsung SM-A556B"}, "dest_path": "Archiv"}, nil, 200)
	e.do(anna, "POST", "/api/v1/files/delete", map[string]any{"paths": []string{"Archiv/Samsung SM-A556B"}}, nil, 200)
	var trash []core.TrashItem
	e.do(anna, "GET", "/api/v1/trash", nil, &trash, 200)
	if len(trash) != 1 {
		t.Fatalf("trash: %+v", trash)
	}
	e.do(anna, "POST", "/api/v1/trash/restore", map[string]any{"ids": []int64{trash[0].ID}}, nil, 200)
	e.do(anna, "GET", "/api/v1/files/stat?space=home&path=Archiv/Samsung%20SM-A556B/DCIM/Camera/bild.jpg", nil, nil, 200)

	// Folders can be moved directly on the server PC as well.
	home := filepath.Join(e.dir, "storage", "users", "anna")
	if err := os.Rename(filepath.Join(home, "Archiv", "Samsung SM-A556B"), filepath.Join(home, "Archiv", "Handy")); err != nil {
		t.Fatalf("moving the folder on disk: %v", err)
	}

	// Changes below the moved folder are still noticed without a listing.
	var me struct{ ID int64 }
	e.do(anna, "GET", "/api/v1/me", nil, &me, 200)
	events, cancel := e.core.Events.Subscribe(me.ID)
	defer cancel()
	camera := filepath.Join(home, "Archiv", "Handy", "DCIM", "Camera")
	deadline := time.After(20 * time.Second)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
wait:
	for n := 0; ; {
		select {
		case ev := <-events:
			if d, ok := ev.Data.(map[string]any); ok && ev.Type == "changed" && d["path"] == "Archiv/Handy/DCIM/Camera" {
				break wait
			}
			continue
		case <-tick.C:
		case <-deadline:
			t.Fatal("a new file below the moved folder was not noticed")
		}
		n++
		os.WriteFile(filepath.Join(camera, fmt.Sprintf("neu%d.jpg", n)), []byte("neu"), 0o644)
	}

	// And the whole tree can still be deleted.
	e.do(anna, "POST", "/api/v1/files/delete", map[string]any{"paths": []string{"Archiv"}}, nil, 200)
	if _, err := os.Stat(filepath.Join(home, "Archiv")); !os.IsNotExist(err) {
		t.Fatalf("Archiv still on disk: %v", err)
	}
}
