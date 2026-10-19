package api

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/erikedits/homelydrive/internal/server/core"
)

func TestAvatar(t *testing.T) {
	e := newEnv(t, 1)
	anna := e.user("anna", false)
	ben := e.user("ben", false)
	var me struct {
		ID     int64
		Avatar int64
	}
	e.do(anna, "GET", "/api/v1/me", nil, &me, 200)
	e.do(ben, "GET", fmt.Sprintf("/api/v1/users/%d/avatar", me.ID), nil, nil, 404)

	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for x := 0; x < 640; x++ {
		for y := 0; y < 480; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var pic bytes.Buffer
	png.Encode(&pic, img)
	e.do(anna, "PUT", "/api/v1/me/avatar", pic.Bytes(), &me, 200)
	if me.Avatar == 0 {
		t.Fatal("avatar not set")
	}
	var got []byte
	e.do(ben, "GET", fmt.Sprintf("/api/v1/users/%d/avatar?v=%d", me.ID, me.Avatar), nil, &got, 200)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatalf("avatar: %v %+v", err, cfg)
	}

	// Others see it next to the name.
	var users []core.UserBrief
	e.do(ben, "GET", "/api/v1/users", nil, &users, 200)
	if len(users) != 1 || users[0].Avatar != me.Avatar {
		t.Fatalf("users: %+v", users)
	}
	e.upload(ben, "home", "liste.txt", []byte("Einkauf"))
	var share core.Share
	e.do(ben, "POST", "/api/v1/shares", map[string]any{"path": "liste.txt", "user_id": me.ID}, &share, 200)
	if share.TargetAvatar != me.Avatar {
		t.Fatalf("share: %+v", share)
	}

	e.do(anna, "PUT", "/api/v1/me/avatar", []byte("kein Bild"), nil, 400)
	e.do(anna, "DELETE", "/api/v1/me/avatar", nil, &me, 200)
	if me.Avatar != 0 {
		t.Fatal("avatar not removed")
	}
	e.do(ben, "GET", fmt.Sprintf("/api/v1/users/%d/avatar", me.ID), nil, nil, 404)
}
