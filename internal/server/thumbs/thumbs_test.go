package thumbs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndEdit(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 200; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	src := filepath.Join(dir, "a.png")
	f, _ := os.Create(src)
	png.Encode(f, img)
	f.Close()
	dst := filepath.Join(dir, "t", "a.jpg")
	if err := Generate(context.Background(), src, "image", dst, 100); err != nil {
		t.Fatal(err)
	}
	tf, _ := os.Open(dst)
	cfg, _ := jpeg.DecodeConfig(tf)
	tf.Close()
	if cfg.Width != 100 || cfg.Height != 50 {
		t.Fatalf("thumb size %dx%d", cfg.Width, cfg.Height)
	}
	out := filepath.Join(dir, "edited.png")
	ext, err := Edit(src, out, 90, &Crop{X: 0, Y: 0, W: 0.5, H: 1})
	if err != nil || ext != "png" {
		t.Fatal(ext, err)
	}
	of, _ := os.Open(out)
	res, _ := png.Decode(of)
	of.Close()
	if b := res.Bounds(); b.Dx() != 200 || b.Dy() != 200 {
		t.Fatalf("edited size %v", b)
	}
	r, _, _, _ := res.At(10, 10).RGBA()
	if r < 0xf000 {
		t.Fatal("crop kept the wrong half")
	}
}

func TestAvatar(t *testing.T) {
	dir := t.TempDir()
	// A wide picture with a red middle: the avatar is its middle square.
	img := image.NewRGBA(image.Rect(0, 0, 900, 300))
	for x := 0; x < 900; x++ {
		for y := 0; y < 300; y++ {
			c := color.RGBA{0, 0, 255, 255}
			if x >= 300 && x < 600 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	src := filepath.Join(dir, "a.png")
	f, _ := os.Create(src)
	png.Encode(f, img)
	f.Close()
	var buf bytes.Buffer
	if err := Avatar(context.Background(), src, &buf, 128); err != nil {
		t.Fatal(err)
	}
	out, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != 128 || b.Dy() != 128 {
		t.Fatalf("avatar size %v", b)
	}
	for _, p := range []image.Point{{2, 2}, {64, 64}, {125, 125}} {
		if r, _, b, _ := out.At(p.X, p.Y).RGBA(); r < 0xc000 || b > 0x4000 {
			t.Fatalf("pixel %v is not red: %v", p, out.At(p.X, p.Y))
		}
	}
	os.WriteFile(src, []byte("kein Bild"), 0o644)
	if err := Avatar(context.Background(), src, &buf, 128); err != ErrUnsupported {
		t.Fatalf("garbage: %v", err)
	}
}
