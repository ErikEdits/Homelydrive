package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/erikedits/homelydrive/internal/server/storage"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
)

// ----- Profile pictures -----

const (
	avatarSize      = 256
	maxAvatarUpload = 30 << 20
)

func (c *Core) avatarDir() string { return filepath.Join(c.DataDir, "avatars") }

func (c *Core) avatarFile(userID int64) string {
	return filepath.Join(c.avatarDir(), strconv.FormatInt(userID, 10)+".jpg")
}

// SetAvatar makes the uploaded image the user's profile picture. It is cut
// to a square from its middle and stored small.
func (c *Core) SetAvatar(ctx context.Context, u *User, r io.Reader) error {
	if err := os.MkdirAll(c.avatarDir(), 0o750); err != nil {
		return err
	}
	in, err := os.CreateTemp(c.avatarDir(), "upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(in.Name())
	n, err := io.Copy(in, io.LimitReader(r, maxAvatarUpload+1))
	if cerr := in.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxAvatarUpload {
		return badRequest("Das Bild ist zu groß (höchstens 30 MB).")
	}
	out, err := os.CreateTemp(c.avatarDir(), "avatar-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	err = thumbs.Avatar(ctx, in.Name(), out, avatarSize)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, thumbs.ErrUnsupported) {
		return badRequest("Das Bild kann nicht gelesen werden. Bitte ein Foto als JPG, PNG oder WebP wählen.")
	}
	if err != nil {
		return err
	}
	if err := storage.MoveFile(out.Name(), c.avatarFile(u.ID)); err != nil {
		return err
	}
	if _, err := c.DB.Exec(`UPDATE users SET avatar = ? WHERE id = ?`, now(), u.ID); err != nil {
		return err
	}
	c.clearAuthCache()
	return nil
}

// DeleteAvatar removes the user's profile picture.
func (c *Core) DeleteAvatar(u *User) error {
	if err := os.Remove(c.avatarFile(u.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := c.DB.Exec(`UPDATE users SET avatar = 0 WHERE id = ?`, u.ID); err != nil {
		return err
	}
	c.clearAuthCache()
	return nil
}

// AvatarPath returns the file of a user's profile picture.
func (c *Core) AvatarPath(userID int64) (string, error) {
	var v int64
	if err := c.DB.QueryRow(`SELECT avatar FROM users WHERE id = ?`, userID).Scan(&v); err != nil || v == 0 {
		return "", ErrNotFound
	}
	p := c.avatarFile(userID)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}
