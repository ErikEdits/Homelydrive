package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User is a Homelydrive account.
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	QuotaBytes  int64  `json:"quota_bytes"` // 0 = unlimited
	Lang        string `json:"lang"`
	HasVault    bool   `json:"has_vault"`
	CreatedAt   int64  `json:"created_at"`
}

// Name returns the display name or the username.
func (u *User) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("homelydrive"), bcrypt.DefaultCost)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

const userCols = `id, username, display_name, is_admin, quota_bytes, lang, vault_key IS NOT NULL, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.QuotaBytes, &u.Lang, &u.HasVault, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserCount returns the number of accounts (0 means first-run setup).
func (c *Core) UserCount() int {
	var n int
	c.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// NewUser describes an account to create.
type NewUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	IsAdmin     bool   `json:"is_admin"`
	QuotaBytes  *int64 `json:"quota_bytes"` // nil = default from config
}

func validatePassword(pw string) error {
	if len(pw) < 6 {
		return badRequest("Das Passwort muss mindestens 6 Zeichen haben.")
	}
	if len(pw) > 72 {
		return badRequest("Das Passwort darf höchstens 72 Zeichen haben.")
	}
	return nil
}

// CreateUser creates an account and its own drive ("Meine Ablage").
func (c *Core) CreateUser(n NewUser) (*User, error) {
	n.Username = strings.TrimSpace(n.Username)
	if !usernameRe.MatchString(n.Username) {
		return nil, badRequest("Ungültiger Benutzername (erlaubt: Buchstaben, Ziffern, . _ -, max. 32 Zeichen).")
	}
	if err := validatePassword(n.Password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(n.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	quota := c.Cfg.Get().DefaultQuotaGB << 30
	if n.QuotaBytes != nil {
		quota = *n.QuotaBytes
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO users(username, display_name, password_hash, is_admin, quota_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, n.Username, strings.TrimSpace(n.DisplayName), string(hash), n.IsAdmin, quota, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO spaces(kind, name, owner_id, created_at) VALUES ('home', ?, ?, ?)`,
		strings.ToLower(n.Username), id, now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	c.Log.Info("user created", "user", n.Username, "admin", n.IsAdmin)
	return c.GetUser(id)
}

// GetUser loads an account by id.
func (c *Core) GetUser(id int64) (*User, error) {
	return scanUser(c.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetUserByName loads an account by user name (case-insensitive).
func (c *Core) GetUserByName(name string) (*User, error) {
	return scanUser(c.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, name))
}

// ListUsers returns all accounts.
func (c *Core) ListUsers() ([]*User, error) {
	rows, err := c.DB.Query(`SELECT ` + userCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserUpdate holds optional changes to an account.
type UserUpdate struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	IsAdmin     *bool   `json:"is_admin"`
	QuotaBytes  *int64  `json:"quota_bytes"`
	Lang        *string `json:"lang"`
}

// UpdateUser changes an account. Changing the password logs out all devices
// except keepToken.
func (c *Core) UpdateUser(id int64, upd UserUpdate, keepToken string) error {
	if upd.DisplayName != nil {
		if _, err := c.DB.Exec(`UPDATE users SET display_name = ? WHERE id = ?`, strings.TrimSpace(*upd.DisplayName), id); err != nil {
			return err
		}
	}
	if upd.Lang != nil {
		l := *upd.Lang
		if l != "de" && l != "en" {
			l = ""
		}
		c.DB.Exec(`UPDATE users SET lang = ? WHERE id = ?`, l, id)
	}
	if upd.IsAdmin != nil {
		if !*upd.IsAdmin {
			var admins int
			c.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1 AND id <> ?`, id).Scan(&admins)
			if admins == 0 {
				return badRequest("Es muss mindestens ein Admin übrig bleiben.")
			}
		}
		c.DB.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, *upd.IsAdmin, id)
	}
	if upd.QuotaBytes != nil {
		if *upd.QuotaBytes < 0 {
			return badRequest("Ungültiges Speicherlimit.")
		}
		c.DB.Exec(`UPDATE users SET quota_bytes = ?, warned_quota = 0 WHERE id = ?`, *upd.QuotaBytes, id)
	}
	if upd.Password != nil {
		if err := validatePassword(*upd.Password); err != nil {
			return err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*upd.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		c.DB.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), id)
		c.DB.Exec(`DELETE FROM tokens WHERE user_id = ? AND token_hash <> ?`, id, hashToken(keepToken))
		c.clearAuthCache()
	}
	return nil
}

// DeleteUser removes an account. Its files stay on disk unless deleteFiles.
func (c *Core) DeleteUser(id int64, deleteFiles bool) error {
	u, err := c.GetUser(id)
	if err != nil {
		return err
	}
	if u.IsAdmin {
		var admins int
		c.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1 AND id <> ?`, id).Scan(&admins)
		if admins == 0 {
			return badRequest("Der letzte Admin kann nicht gelöscht werden.")
		}
	}
	home, err := c.HomeSpace(id)
	if err == nil && deleteFiles {
		for _, r := range home.Union.Roots {
			removeAllQuiet(r.Dir)
		}
	}
	c.deleteVaultFiles(id)
	if _, err := c.DB.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	c.forgetSpaces()
	c.clearAuthCache()
	c.Log.Info("user deleted", "user", u.Username, "files_deleted", deleteFiles)
	return nil
}

// CheckPassword verifies a user's password.
func (c *Core) CheckPassword(username, password string) (*User, error) {
	var id int64
	var hash string
	err := c.DB.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, username).Scan(&id, &hash)
	if err != nil {
		// Spend the same time as a real check to not reveal valid names.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrUnauthorized
	}
	return c.GetUser(id)
}

// Login checks the credentials and returns a new device token. Tokens do not
// expire; they stay valid until the user logs out (as requested).
func (c *Core) Login(username, password, device string, admin bool) (string, *User, error) {
	u, err := c.CheckPassword(username, password)
	if err != nil {
		return "", nil, err
	}
	if admin && !u.IsAdmin {
		return "", nil, ErrForbidden
	}
	tok, err := c.CreateToken(u.ID, device, admin)
	if err != nil {
		return "", nil, err
	}
	c.Log.Info("login", "user", u.Username, "device", device, "admin", admin)
	return tok, u, nil
}

// CreateToken issues a token for a user.
func (c *Core) CreateToken(userID int64, device string, admin bool) (string, error) {
	tok := "hd_" + randomToken(32)
	if len(device) > 100 {
		device = device[:100]
	}
	_, err := c.DB.Exec(`INSERT INTO tokens(user_id, token_hash, device, admin, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, hashToken(tok), device, admin, now(), now())
	return tok, err
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// Session is an authenticated request context.
type Session struct {
	User    *User
	Token   string
	IsAdmin bool // token was issued by the admin (server window) login
}

// Authenticate resolves a token.
func (c *Core) Authenticate(tok string) (*Session, error) {
	if !strings.HasPrefix(tok, "hd_") {
		return nil, ErrUnauthorized
	}
	var uid, tid, last int64
	var admin bool
	err := c.DB.QueryRow(`SELECT id, user_id, admin, last_used_at FROM tokens WHERE token_hash = ?`, hashToken(tok)).Scan(&tid, &uid, &admin, &last)
	if err != nil {
		return nil, ErrUnauthorized
	}
	u, err := c.GetUser(uid)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if now()-last > int64(time.Hour/time.Millisecond) {
		c.DB.Exec(`UPDATE tokens SET last_used_at = ? WHERE id = ?`, now(), tid)
	}
	return &Session{User: u, Token: tok, IsAdmin: admin && u.IsAdmin}, nil
}

// Logout invalidates a token.
func (c *Core) Logout(tok string) {
	c.DB.Exec(`DELETE FROM tokens WHERE token_hash = ?`, hashToken(tok))
}

type authCacheEntry struct {
	userID int64
	until  time.Time
}

// BasicAuth verifies a user name and password (or device token) for WebDAV,
// caching successful checks briefly because bcrypt is slow.
func (c *Core) BasicAuth(username, password string) (*User, error) {
	key := hashToken(username + "\x00" + password)
	c.authCacheMu.Lock()
	e, ok := c.authCache[key]
	c.authCacheMu.Unlock()
	if ok && time.Now().Before(e.until) {
		return c.GetUser(e.userID)
	}
	var u *User
	if s, err := c.Authenticate(password); err == nil && strings.EqualFold(s.User.Username, username) {
		u = s.User
	} else {
		u, err = c.CheckPassword(username, password)
		if err != nil {
			return nil, err
		}
	}
	c.authCacheMu.Lock()
	c.authCache[key] = authCacheEntry{userID: u.ID, until: time.Now().Add(5 * time.Minute)}
	c.authCacheMu.Unlock()
	return u, nil
}

func (c *Core) clearAuthCache() {
	c.authCacheMu.Lock()
	c.authCache = map[string]authCacheEntry{}
	c.authCacheMu.Unlock()
}

// Devices lists the logged in devices (tokens) of a user.
type Device struct {
	ID       int64  `json:"id"`
	Device   string `json:"device"`
	Created  int64  `json:"created_at"`
	LastUsed int64  `json:"last_used_at"`
	Current  bool   `json:"current"`
}

// ListDevices returns the user's sessions.
func (c *Core) ListDevices(userID int64, currentToken string) ([]Device, error) {
	rows, err := c.DB.Query(`SELECT id, device, created_at, last_used_at, token_hash FROM tokens WHERE user_id = ? AND admin = 0 ORDER BY last_used_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cur := hashToken(currentToken)
	out := []Device{}
	for rows.Next() {
		var d Device
		var h string
		if err := rows.Scan(&d.ID, &d.Device, &d.Created, &d.LastUsed, &h); err != nil {
			return nil, err
		}
		d.Current = h == cur
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice logs out one of the user's devices.
func (c *Core) RevokeDevice(userID, id int64) error {
	_, err := c.DB.Exec(`DELETE FROM tokens WHERE id = ? AND user_id = ?`, id, userID)
	c.clearAuthCache()
	return err
}
