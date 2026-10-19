package core

import (
	"encoding/json"
	"sync"
)

// Event is pushed to connected clients (web UI, PC app) via Server-Sent Events.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker fans out events to the subscribers of each user.
type Broker struct {
	mu   sync.Mutex
	subs map[int64]map[chan Event]struct{}
}

// NewBroker creates an empty broker.
func NewBroker() *Broker { return &Broker{subs: map[int64]map[chan Event]struct{}{}} }

// Subscribe returns a channel receiving the user's events and a cancel func.
func (b *Broker) Subscribe(userID int64) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs[userID] == nil {
		b.subs[userID] = map[chan Event]struct{}{}
	}
	b.subs[userID][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[userID], ch)
		if len(b.subs[userID]) == 0 {
			delete(b.subs, userID)
		}
		b.mu.Unlock()
	}
}

// Publish sends an event to all connections of a user (dropping it for slow
// connections rather than blocking).
func (b *Broker) Publish(userID int64, ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[userID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Online returns the number of connected event streams.
func (b *Broker) Online() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.subs {
		n += len(m)
	}
	return n
}

// publishChange tells everyone who can see a space that a folder changed, so
// open file lists refresh themselves.
func (c *Core) publishChange(spaceID int64, parent string) {
	ev := Event{Type: "changed", Data: map[string]any{"space_id": spaceID, "path": parent}}
	for _, uid := range c.usersWithAccess(spaceID) {
		c.Events.Publish(uid, ev)
	}
}

// Notification is a message in the bell menu of the apps.
type Notification struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	Read      bool            `json:"read"`
	CreatedAt int64           `json:"created_at"`
}

// Notify stores a notification and pushes it to the user's devices.
func (c *Core) Notify(userID int64, kind string, data map[string]any) {
	b, _ := json.Marshal(data)
	res, err := c.DB.Exec(`INSERT INTO notifications(user_id, kind, data, created_at) VALUES (?, ?, ?, ?)`, userID, kind, string(b), now())
	if err != nil {
		return
	}
	id, _ := res.LastInsertId()
	c.Events.Publish(userID, Event{Type: "notification", Data: Notification{ID: id, Kind: kind, Data: b, CreatedAt: now()}})
	// Keep the list short.
	c.DB.Exec(`DELETE FROM notifications WHERE user_id = ? AND id NOT IN (SELECT id FROM notifications WHERE user_id = ? ORDER BY id DESC LIMIT 200)`, userID, userID)
}

func (c *Core) notifyAdmins(kind string, data map[string]any) {
	rows, err := c.DB.Query(`SELECT id FROM users WHERE is_admin = 1`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		c.Notify(id, kind, data)
	}
}

// ListNotifications returns notifications newer than afterID.
func (c *Core) ListNotifications(userID, afterID int64, limit int) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := c.DB.Query(`SELECT id, kind, data, read, created_at FROM notifications WHERE user_id = ? AND id > ? ORDER BY id DESC LIMIT ?`, userID, afterID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var data string
		if err := rows.Scan(&n.ID, &n.Kind, &data, &n.Read, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		n.Data = json.RawMessage(data)
		out = append(out, n)
	}
	var unread int
	c.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read = 0`, userID).Scan(&unread)
	return out, unread, rows.Err()
}

// MarkNotificationsRead marks the given (or all, if ids is empty) as read.
func (c *Core) MarkNotificationsRead(userID int64, ids []int64) {
	if len(ids) == 0 {
		c.DB.Exec(`UPDATE notifications SET read = 1 WHERE user_id = ?`, userID)
		return
	}
	for _, id := range ids {
		c.DB.Exec(`UPDATE notifications SET read = 1 WHERE user_id = ? AND id = ?`, userID, id)
	}
}

// ----- Activity log -----

// Activity is one entry of the activity log ("Aktivitäten").
type Activity struct {
	ID          int64  `json:"id"`
	Actor       string `json:"actor"`
	ActorID     int64  `json:"actor_id,omitempty"`
	ActorAvatar int64  `json:"actor_avatar,omitempty"`
	Action      string `json:"action"`
	Ref         string `json:"space,omitempty"`
	Path        string `json:"path"`
	Details     string `json:"details,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// logActivity records an action. actor is the user (nil for link uploads).
func (c *Core) logActivity(u *User, actor string, spaceID int64, action, p, details string) {
	var uid any
	if u != nil {
		uid = u.ID
		actor = u.Name()
	}
	c.DB.Exec(`INSERT INTO activity(user_id, actor, space_id, action, path, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uid, actor, spaceID, action, p, details, now())
}

// ListActivity returns recent activity in everything the user can see.
func (c *Core) ListActivity(u *User, beforeID int64, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	scopes := c.scopes(u)
	where, args := scopeSQL(scopes, false, "a")
	where = "(" + where + " OR a.user_id = ?)"
	args = append(args, u.ID)
	q := `SELECT a.id, a.actor, COALESCE(a.user_id, 0), COALESCE(au.avatar, 0), a.space_id, a.action, a.path, a.details, a.created_at
		FROM activity a LEFT JOIN users au ON au.id = a.user_id
		WHERE a.id < ? AND ` + where + ` ORDER BY a.id DESC LIMIT ?`
	rows, err := c.DB.Query(q, append(append([]any{beforeID}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var spaceID int64
		if err := rows.Scan(&a.ID, &a.Actor, &a.ActorID, &a.ActorAvatar, &spaceID, &a.Action, &a.Path, &a.Details, &a.CreatedAt); err != nil {
			return nil, err
		}
		if ref, rel, ok := refFor(scopes, spaceID, a.Path); ok {
			a.Ref, a.Path = ref, rel
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
