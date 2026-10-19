package core

import (
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/erikedits/homelydrive/internal/server/fsname"
)

// Share is a file or folder shared with another user of the server.
type Share struct {
	ID           int64  `json:"id"`
	Space        string `json:"space,omitempty"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	TargetID     int64  `json:"target_id"`
	TargetName   string `json:"target_name"`
	TargetAvatar int64  `json:"target_avatar,omitempty"`
	OwnerName    string `json:"owner_name,omitempty"`
	CanWrite     bool   `json:"can_write"`
	CreatedAt    int64  `json:"created_at"`
}

// UserBrief is shown in the share dialog.
type UserBrief struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      int64  `json:"avatar,omitempty"`
}

// ListUserBriefs lists all other users (for choosing whom to share with).
func (c *Core) ListUserBriefs(exclude int64) ([]UserBrief, error) {
	rows, err := c.DB.Query(`SELECT id, username, display_name, avatar FROM users WHERE id <> ? ORDER BY username`, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserBrief{}
	for rows.Next() {
		var u UserBrief
		rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Avatar)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SharesOf lists who an item is shared with.
func (c *Core) SharesOf(a *Access, rel string) ([]Share, error) {
	rel, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	full := a.Full(rel)
	rows, err := c.DB.Query(`SELECT s.id, s.path, s.is_dir, s.target_user_id, COALESCE(NULLIF(u.display_name, ''), u.username), u.avatar, s.can_write, s.created_at
		FROM shares s JOIN users u ON u.id = s.target_user_id WHERE s.space_id = ? AND s.path = ? ORDER BY u.username`, a.Space.ID, full)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var s Share
		if err := rows.Scan(&s.ID, &s.Path, &s.IsDir, &s.TargetID, &s.TargetName, &s.TargetAvatar, &s.CanWrite, &s.CreatedAt); err != nil {
			return nil, err
		}
		_, s.Name = fsname.Split(s.Path)
		s.Path = a.Rel(s.Path)
		s.Space = a.Ref
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreateShare shares an item with another user and notifies them.
func (c *Core) CreateShare(a *Access, rel string, targetID int64, canWrite bool) (*Share, error) {
	if !a.CanShare() {
		return nil, ErrForbidden
	}
	rel, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	full := a.Full(rel)
	if full == "" {
		return nil, badRequest("Der ganze Bereich kann nicht geteilt werden – bitte einen Ordner wählen.")
	}
	if targetID == a.User.ID {
		return nil, badRequest("Du kannst nicht mit dir selbst teilen.")
	}
	target, err := c.GetUser(targetID)
	if err != nil {
		return nil, err
	}
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return nil, err
	}
	_, err = c.DB.Exec(`INSERT INTO shares(owner_id, space_id, path, is_dir, target_user_id, can_write, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(space_id, path, target_user_id) DO UPDATE SET can_write = excluded.can_write`,
		a.User.ID, a.Space.ID, full, r.IsDir, targetID, canWrite, now())
	if err != nil {
		return nil, err
	}
	var id int64
	c.DB.QueryRow(`SELECT id FROM shares WHERE space_id = ? AND path = ? AND target_user_id = ?`, a.Space.ID, full, targetID).Scan(&id)
	c.logActivity(a.User, "", a.Space.ID, "share", full, target.Username)
	c.Notify(targetID, "share", map[string]any{
		"from": a.User.Name(), "name": r.Name, "is_dir": r.IsDir, "space": "share:" + strconv.FormatInt(id, 10), "can_write": canWrite,
	})
	c.Events.Publish(targetID, Event{Type: "roots_changed", Data: nil})
	return &Share{ID: id, Path: rel, Name: r.Name, IsDir: r.IsDir, TargetID: targetID, TargetName: target.Name(), TargetAvatar: target.Avatar, CanWrite: canWrite, CreatedAt: now()}, nil
}

// DeleteShare stops sharing. The owner or the recipient may do this.
func (c *Core) DeleteShare(u *User, id int64) error {
	var owner, target, spaceID int64
	var p string
	err := c.DB.QueryRow(`SELECT owner_id, target_user_id, space_id, path FROM shares WHERE id = ?`, id).Scan(&owner, &target, &spaceID, &p)
	if err != nil {
		return ErrNotFound
	}
	if owner != u.ID && target != u.ID {
		// Team members with write access may manage shares of team items.
		var ok bool
		c.DB.QueryRow(`SELECT can_write FROM space_members WHERE space_id = ? AND user_id = ?`, spaceID, u.ID).Scan(&ok)
		if !ok {
			return ErrNotFound
		}
	}
	c.DB.Exec(`DELETE FROM shares WHERE id = ?`, id)
	c.logActivity(u, "", spaceID, "unshare", p, "")
	c.Events.Publish(target, Event{Type: "roots_changed"})
	return nil
}

// OutgoingShares lists everything the user shared with others.
func (c *Core) OutgoingShares(u *User) ([]Share, error) {
	scopes := c.scopes(u)
	rows, err := c.DB.Query(`SELECT s.id, s.space_id, s.path, s.is_dir, s.target_user_id, COALESCE(NULLIF(t.display_name, ''), t.username), t.avatar, s.can_write, s.created_at
		FROM shares s JOIN users t ON t.id = s.target_user_id WHERE s.owner_id = ? ORDER BY s.created_at DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var s Share
		var spaceID int64
		if err := rows.Scan(&s.ID, &spaceID, &s.Path, &s.IsDir, &s.TargetID, &s.TargetName, &s.TargetAvatar, &s.CanWrite, &s.CreatedAt); err != nil {
			return nil, err
		}
		_, s.Name = fsname.Split(s.Path)
		if ref, rel, ok := refFor(scopes, spaceID, s.Path); ok {
			s.Space, s.Path = ref, rel
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ----- Links -----

// Link is a public link (download link or upload link). Links only work for
// devices inside the tailnet because the server is only reachable there.
type Link struct {
	ID           int64  `json:"id"`
	Token        string `json:"token"`
	Kind         string `json:"kind"` // download | upload
	Space        string `json:"space,omitempty"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	HasPassword  bool   `json:"has_password"`
	ExpiresAt    int64  `json:"expires_at"`
	MaxDownloads int64  `json:"max_downloads"`
	Downloads    int64  `json:"downloads"`
	CreatedAt    int64  `json:"created_at"`

	ownerID  int64
	spaceID  int64
	fullPath string
	pwHash   string
}

// LinkOptions are set when creating or editing a link.
type LinkOptions struct {
	Kind         string  `json:"kind"`
	Password     *string `json:"password"` // nil = unchanged, "" = remove
	ExpiresAt    *int64  `json:"expires_at"`
	MaxDownloads *int64  `json:"max_downloads"`
}

const linkCols = `id, token, kind, owner_id, space_id, path, is_dir, password_hash, expires_at, max_downloads, downloads, created_at`

func scanLink(row interface{ Scan(...any) error }) (*Link, error) {
	l := &Link{}
	err := row.Scan(&l.ID, &l.Token, &l.Kind, &l.ownerID, &l.spaceID, &l.fullPath, &l.IsDir, &l.pwHash, &l.ExpiresAt, &l.MaxDownloads, &l.Downloads, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.HasPassword = l.pwHash != ""
	_, l.Name = fsname.Split(l.fullPath)
	l.Path = l.fullPath
	return l, nil
}

// CreateLink creates a download or upload link for an item.
func (c *Core) CreateLink(a *Access, rel string, opt LinkOptions) (*Link, error) {
	if !a.CanShare() {
		return nil, ErrForbidden
	}
	rel, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	full := a.Full(rel)
	if opt.Kind != "upload" {
		opt.Kind = "download"
	}
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return nil, err
	}
	if opt.Kind == "upload" && !r.IsDir {
		return nil, badRequest("Upload-Links gehen nur für Ordner.")
	}
	if full == "" && opt.Kind == "download" {
		return nil, badRequest("Bitte einen Ordner oder eine Datei wählen.")
	}
	token := randomToken(15)
	res, err := c.DB.Exec(`INSERT INTO links(token, owner_id, space_id, path, is_dir, kind, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		token, a.User.ID, a.Space.ID, full, r.IsDir, opt.Kind, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := c.UpdateLink(a.User, id, opt); err != nil {
		c.DB.Exec(`DELETE FROM links WHERE id = ?`, id)
		return nil, err
	}
	c.logActivity(a.User, "", a.Space.ID, "link_create", full, opt.Kind)
	l, err := c.getLink(id)
	if err != nil {
		return nil, err
	}
	l.Space, l.Path = a.Ref, rel
	return l, nil
}

func (c *Core) getLink(id int64) (*Link, error) {
	return scanLink(c.DB.QueryRow(`SELECT `+linkCols+` FROM links WHERE id = ?`, id))
}

// UpdateLink changes password, expiry or download limit of a link.
func (c *Core) UpdateLink(u *User, id int64, opt LinkOptions) error {
	l, err := c.getLink(id)
	if err != nil || l.ownerID != u.ID {
		return ErrNotFound
	}
	if opt.Password != nil {
		hash := ""
		if *opt.Password != "" {
			if len(*opt.Password) > 72 {
				return badRequest("Passwort zu lang.")
			}
			b, err := bcrypt.GenerateFromPassword([]byte(*opt.Password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			hash = string(b)
		}
		c.DB.Exec(`UPDATE links SET password_hash = ? WHERE id = ?`, hash, id)
	}
	if opt.ExpiresAt != nil {
		if *opt.ExpiresAt < 0 {
			return badRequest("Ungültiges Ablaufdatum.")
		}
		c.DB.Exec(`UPDATE links SET expires_at = ? WHERE id = ?`, *opt.ExpiresAt, id)
	}
	if opt.MaxDownloads != nil {
		if *opt.MaxDownloads < 0 {
			return badRequest("Ungültige Anzahl.")
		}
		c.DB.Exec(`UPDATE links SET max_downloads = ? WHERE id = ?`, *opt.MaxDownloads, id)
	}
	return nil
}

// DeleteLink removes a link.
func (c *Core) DeleteLink(u *User, id int64) error {
	l, err := c.getLink(id)
	if err != nil || l.ownerID != u.ID {
		return ErrNotFound
	}
	c.DB.Exec(`DELETE FROM links WHERE id = ?`, id)
	c.logActivity(u, "", l.spaceID, "link_delete", l.fullPath, l.Kind)
	return nil
}

// ListLinks returns the user's links, optionally only those of one item.
func (c *Core) ListLinks(u *User, a *Access, rel string) ([]Link, error) {
	q := `SELECT ` + linkCols + ` FROM links WHERE owner_id = ?`
	args := []any{u.ID}
	if a != nil {
		r, err := relPath(rel)
		if err != nil {
			return nil, err
		}
		q += ` AND space_id = ? AND path = ?`
		args = append(args, a.Space.ID, a.Full(r))
	}
	rows, err := c.DB.Query(q+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := c.scopes(u)
	out := []Link{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		if ref, rel, ok := refFor(scopes, l.spaceID, l.fullPath); ok {
			l.Space, l.Path = ref, rel
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// LinkByToken loads a link for public access and checks expiry and limits.
func (c *Core) LinkByToken(token string) (*Link, error) {
	l, err := scanLink(c.DB.QueryRow(`SELECT `+linkCols+` FROM links WHERE token = ?`, token))
	if err != nil {
		return nil, err
	}
	if l.ExpiresAt > 0 && now() > l.ExpiresAt {
		return nil, ErrLinkExpired
	}
	if l.Kind == "download" && l.MaxDownloads > 0 && l.Downloads >= l.MaxDownloads {
		return nil, ErrLinkExpired
	}
	return l, nil
}

// ErrLinkExpired is returned for expired or used up links.
var ErrLinkExpired = errors.New("link expired")

// CheckLinkPassword verifies the password of a protected link.
func (c *Core) CheckLinkPassword(l *Link, pw string) bool {
	if l.pwHash == "" {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(l.pwHash), []byte(pw)) == nil
}

// LinkSecret is the value stored in a cookie after the password was entered.
func (c *Core) LinkSecret(l *Link) string {
	return hashToken("link:" + l.Token + ":" + l.pwHash)[:32]
}

// LinkAccess returns an access object for public link operations.
func (c *Core) LinkAccess(l *Link) (*Access, error) {
	s, err := c.GetSpace(l.spaceID)
	if err != nil {
		return nil, err
	}
	if _, err := c.GetUser(l.ownerID); err != nil {
		return nil, err
	}
	return &Access{Ref: "link", Space: s, Base: l.fullPath, CanWrite: l.Kind == "upload", Label: l.Name}, nil
}

// CountLinkDownload increments the download counter.
func (c *Core) CountLinkDownload(l *Link) {
	c.DB.Exec(`UPDATE links SET downloads = downloads + 1 WHERE id = ?`, l.ID)
}

// LinkOwnerName returns who shared a link (shown on the public page).
func (c *Core) LinkOwnerName(l *Link) string {
	if u, err := c.GetUser(l.ownerID); err == nil {
		return u.Name()
	}
	return ""
}

// CreateLinkUpload starts an upload through an upload link.
func (c *Core) CreateLinkUpload(l *Link, req UploadRequest) (*UploadStatus, error) {
	if l.Kind != "upload" {
		return nil, ErrForbidden
	}
	a, err := c.LinkAccess(l)
	if err != nil {
		return nil, err
	}
	// Uploaders may only add files directly or in sub folders they create;
	// they never see what is already there.
	req.VaultParent = nil
	req.Space = ""
	return c.createUpload(a, uploadOwner{link: l}, req)
}

var linkNotifyMu sync.Mutex
var linkNotified = map[int64]time.Time{}

func (c *Core) notifyLinkUpload(l *Link, name string) {
	linkNotifyMu.Lock()
	last := linkNotified[l.ID]
	if time.Since(last) < 10*time.Minute {
		linkNotifyMu.Unlock()
		return
	}
	linkNotified[l.ID] = time.Now()
	linkNotifyMu.Unlock()
	scopes := c.scopes(&User{ID: l.ownerID})
	ref, rel, _ := refFor(scopes, l.spaceID, l.fullPath)
	c.Notify(l.ownerID, "link_upload", map[string]any{"name": name, "folder": l.Name, "space": ref, "path": rel})
}
