// Package remote is the HTTP client the PC app uses to talk to the
// Homelydrive server (the same JSON API as the web UI and Android app).
package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one server with one device token.
type Client struct {
	Base  string // e.g. http://meinpc.tail1234.ts.net:8420
	Token string
	HTTP  *http.Client
}

// New creates a client with sensible timeouts for long uploads.
func New(base, token string) *Client {
	tr := &http.Transport{
		Proxy:                 nil, // tailnet traffic never goes through a proxy
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		TLSHandshakeTimeout:   15 * time.Second,
	}
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Transport: tr}}
}

// Error is an error answer of the server.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"error"`
	Offset  *int64 `json:"offset"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// IsNetwork reports whether err means the server could not be reached.
func IsNetwork(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Status == http.StatusBadGateway || e.Status == http.StatusServiceUnavailable || e.Status == http.StatusGatewayTimeout
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

// IsCode reports whether err is a server error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// StatusOf returns the HTTP status of a server error (0 otherwise).
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func (c *Client) req(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	u := c.Base + "/api/v1" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	r.Header.Set("User-Agent", "Homelydrive-PC")
	return r, nil
}

func decodeError(resp *http.Response) error {
	e := &Error{Status: resp.StatusCode}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	json.Unmarshal(b, e)
	if e.Message == "" && len(b) > 0 && len(b) < 300 {
		e.Message = strings.TrimSpace(string(b))
	}
	return e
}

// Do sends a JSON request and decodes the JSON answer into out.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	r, err := c.req(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ----- types -----

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
	IsAdmin     bool   `json:"is_admin"`
	Quota       int64  `json:"quota"`
	Used        int64  `json:"used"`
	Lang        string `json:"lang"`
}

type FileInfo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Space    string `json:"space,omitempty"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"`
	Mime     string `json:"mime,omitempty"`
	Category string `json:"category,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	CanWrite bool   `json:"can_write"`
}

type List struct {
	Space    string     `json:"space"`
	SpaceID  int64      `json:"space_id"`
	Base     string     `json:"base"`
	CanWrite bool       `json:"can_write"`
	Entries  []FileInfo `json:"entries"`
}

type Root struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	CanWrite bool   `json:"can_write"`
	IsDir    bool   `json:"is_dir"`
	Owner    string `json:"owner,omitempty"`
	SpaceID  int64  `json:"space_id"`
}

type UploadRequest struct {
	Space    string `json:"space"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
	MTime    int64  `json:"mtime,omitempty"`
	Conflict string `json:"conflict,omitempty"`
}

type UploadStatus struct {
	ID     string    `json:"id,omitempty"`
	Status string    `json:"status"`
	Offset int64     `json:"offset"`
	Size   int64     `json:"size"`
	Path   string    `json:"path,omitempty"`
	File   *FileInfo `json:"file,omitempty"`
}

type Link struct {
	ID    int64  `json:"id"`
	Token string `json:"token"`
	Kind  string `json:"kind"`
}

// ----- calls -----

// Ping checks that a Homelydrive server answers at base.
func Ping(ctx context.Context, base string) error {
	c := New(base, "")
	var out struct {
		Name string `json:"name"`
	}
	if err := c.Do(ctx, "GET", "/ping", nil, nil, &out); err != nil {
		return err
	}
	if out.Name != "homelydrive" {
		return errors.New("kein Homelydrive-Server unter dieser Adresse")
	}
	return nil
}

// Login exchanges credentials for a device token.
func Login(ctx context.Context, base, username, password, device string) (string, *User, error) {
	c := New(base, "")
	var out struct {
		Token string `json:"token"`
		User  User   `json:"user"`
	}
	err := c.Do(ctx, "POST", "/auth/login", nil, map[string]string{"username": username, "password": password, "device": device}, &out)
	if err != nil {
		return "", nil, err
	}
	return out.Token, &out.User, nil
}

func (c *Client) Logout(ctx context.Context) error {
	return c.Do(ctx, "POST", "/auth/logout", nil, nil, nil)
}

func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	return &u, c.Do(ctx, "GET", "/me", nil, nil, &u)
}

func (c *Client) Roots(ctx context.Context) ([]Root, error) {
	var r []Root
	return r, c.Do(ctx, "GET", "/roots", nil, nil, &r)
}

func (c *Client) List(ctx context.Context, space, path string) (*List, error) {
	var l List
	return &l, c.Do(ctx, "GET", "/files/list", url.Values{"space": {space}, "path": {path}}, nil, &l)
}

func (c *Client) Stat(ctx context.Context, space, path string) (*FileInfo, error) {
	var f FileInfo
	return &f, c.Do(ctx, "GET", "/files/stat", url.Values{"space": {space}, "path": {path}}, nil, &f)
}

func (c *Client) Mkdir(ctx context.Context, space, path string) error {
	return c.Do(ctx, "POST", "/files/mkdir", nil, map[string]string{"space": space, "path": path}, nil)
}

func (c *Client) Rename(ctx context.Context, space, path, newName string) error {
	return c.Do(ctx, "POST", "/files/rename", nil, map[string]string{"space": space, "path": path, "new_name": newName}, nil)
}

func (c *Client) Move(ctx context.Context, space string, paths []string, destSpace, destPath string) ([]string, error) {
	var out struct {
		Paths []string `json:"paths"`
	}
	err := c.Do(ctx, "POST", "/files/move", nil, map[string]any{"space": space, "paths": paths, "dest_space": destSpace, "dest_path": destPath}, &out)
	return out.Paths, err
}

func (c *Client) Delete(ctx context.Context, space string, paths []string) error {
	return c.Do(ctx, "POST", "/files/delete", nil, map[string]any{"space": space, "paths": paths}, nil)
}

func (c *Client) CreateLink(ctx context.Context, space, path string) (*Link, error) {
	var l Link
	return &l, c.Do(ctx, "POST", "/links", nil, map[string]string{"space": space, "path": path, "kind": "download"}, &l)
}

// LinkURL returns the public URL of a link.
func (c *Client) LinkURL(l *Link) string {
	if l.Kind == "upload" {
		return c.Base + "/u/" + l.Token
	}
	return c.Base + "/s/" + l.Token
}

func (c *Client) CreateUpload(ctx context.Context, req UploadRequest) (*UploadStatus, error) {
	var st UploadStatus
	return &st, c.Do(ctx, "POST", "/uploads", nil, req, &st)
}

func (c *Client) UploadOffset(ctx context.Context, id string) (int64, error) {
	var st UploadStatus
	err := c.Do(ctx, "GET", "/uploads/"+url.PathEscape(id), nil, nil, &st)
	return st.Offset, err
}

func (c *Client) CancelUpload(ctx context.Context, id string) error {
	return c.Do(ctx, "DELETE", "/uploads/"+url.PathEscape(id), nil, nil, nil)
}

// UploadChunk sends n bytes from body starting at offset.
func (c *Client) UploadChunk(ctx context.Context, id string, offset int64, body io.Reader, n int64) (*UploadStatus, error) {
	r, err := c.req(ctx, "PATCH", "/uploads/"+url.PathEscape(id), nil, body)
	if err != nil {
		return nil, err
	}
	r.ContentLength = n
	r.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	r.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, decodeError(resp)
	}
	var st UploadStatus
	return &st, json.NewDecoder(resp.Body).Decode(&st)
}

// Download opens a file for reading, optionally from offset (length < 0 = to the end).
func (c *Client) Download(ctx context.Context, space, path string, offset, length int64) (io.ReadCloser, int64, error) {
	r, err := c.req(ctx, "GET", "/files/content", url.Values{"space": {space}, "path": {path}}, nil)
	if err != nil {
		return nil, 0, err
	}
	if offset > 0 || length >= 0 {
		end := ""
		if length >= 0 {
			end = strconv.FormatInt(offset+length-1, 10)
		}
		r.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", offset, end))
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return io.NopCloser(strings.NewReader("")), 0, nil
		}
		return nil, 0, decodeError(resp)
	}
	return resp.Body, resp.ContentLength, nil
}

// Event is a live event from the server (see core.Event).
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Events streams server events until ctx ends, reconnecting automatically.
func (c *Client) Events(ctx context.Context, fn func(Event)) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := c.events(ctx, fn)
		if ctx.Err() != nil {
			return
		}
		if StatusOf(err) == http.StatusUnauthorized {
			backoff = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) events(ctx context.Context, fn func(Event)) error {
	r, err := c.req(ctx, "GET", "/events", nil, nil)
	if err != nil {
		return err
	}
	cl := &http.Client{Transport: c.HTTP.Transport} // no timeout for the stream
	resp, err := cl.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp)
	}
	fn(Event{Type: "connected"})
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line[6:]), &ev) == nil {
			fn(ev)
		}
	}
	fn(Event{Type: "disconnected"})
	return sc.Err()
}

// DownloadZip downloads a folder as ZIP archive.
func (c *Client) DownloadZip(ctx context.Context, space, path string) (io.ReadCloser, error) {
	r, err := c.req(ctx, "GET", "/files/zip", url.Values{"space": {space}, "path": {path}}, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}
