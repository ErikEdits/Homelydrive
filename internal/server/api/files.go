package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
)

func (s *Server) access(w http.ResponseWriter, sess *core.Session, ref string) *core.Access {
	a, err := s.Core.ResolveAccess(sess.User, ref)
	if err != nil {
		s.fail(w, err)
		return nil
	}
	return a
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	entries, err := s.Core.List(a, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"space":     a.Ref,
		"space_id":  a.Space.ID,
		"base":      a.Base,
		"can_write": a.CanWrite,
		"can_share": a.CanShare(),
		"label":     a.Label,
		"entries":   entries,
	})
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	fi, err := s.Core.Stat(a, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fi)
}

// serveFile streams a file with range support (video seeking, resumable downloads).
func serveFile(w http.ResponseWriter, r *http.Request, f *core.OpenedFile, download bool) {
	h := w.Header()
	mimeType := f.Type.Mime
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	h.Set("Content-Type", mimeType)
	kind := "inline"
	if download || f.Type.Viewer == "download" {
		kind = "attachment"
	}
	h.Set("Content-Disposition", contentDisposition(kind, f.Name))
	// Files are shown in a sandbox so an uploaded HTML or SVG file can never
	// run scripts. PDFs are excluded: browsers refuse to show them in a
	// sandbox, and their PDF viewers isolate documents on their own.
	if mimeType != "application/pdf" {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	}
	h.Set("Cache-Control", "private, no-cache")
	if f.Row.SHA256 != "" {
		h.Set("ETag", `"`+f.Row.SHA256+`"`)
	}
	http.ServeContent(w, r, "", f.Info.ModTime(), f.File)
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	f, err := s.Core.Open(a, r.URL.Query().Get("path"), r.Header.Get("Range") == "" || strings.HasPrefix(r.Header.Get("Range"), "bytes=0-"))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.File.Close()
	serveFile(w, r, f, r.URL.Query().Get("download") == "1")
}

func (s *Server) handlePutContent(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	if r.ContentLength < 0 {
		s.fail(w, &core.BadRequest{Msg: "Content-Length fehlt."})
		return
	}
	fi, err := s.Core.WriteContent(a, r.URL.Query().Get("path"), r.Body, r.ContentLength)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fi)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	p, err := s.Core.Thumb(r.Context(), a, r.URL.Query().Get("path"), int(qInt(r, "size")))
	if err != nil {
		if errors.Is(err, thumbs.ErrUnsupported) {
			http.Error(w, "no thumbnail", http.StatusNotFound)
			return
		}
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

func (s *Server) handleZip(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	r.ParseForm()
	a := s.access(w, sess, r.Form.Get("space"))
	if a == nil {
		return
	}
	paths := r.Form["path"]
	if len(paths) == 0 {
		paths = []string{""}
	}
	name := r.Form.Get("name")
	if name == "" {
		name = "Homelydrive-" + time.Now().Format("2006-01-02") + ".zip"
		if len(paths) == 1 && paths[0] != "" {
			parts := strings.Split(strings.Trim(paths[0], "/"), "/")
			name = parts[len(parts)-1] + ".zip"
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	if err := s.Core.WriteZip(r.Context(), w, a, paths); err != nil {
		s.Log.Warn("zip download aborted", "err", err)
	}
}

type pathReq struct {
	Space string   `json:"space"`
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req pathReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	fi, err := s.Core.Mkdir(a, req.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fi)
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space   string `json:"space"`
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	p, err := s.Core.Rename(a, req.Path, req.NewName)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": p})
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request, sess *core.Session, move bool) {
	var req struct {
		Space     string   `json:"space"`
		Paths     []string `json:"paths"`
		DestSpace string   `json:"dest_space"`
		DestPath  string   `json:"dest_path"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	src := s.access(w, sess, req.Space)
	if src == nil {
		return
	}
	if req.DestSpace == "" {
		req.DestSpace = req.Space
	}
	dst := s.access(w, sess, req.DestSpace)
	if dst == nil {
		return
	}
	paths, err := s.Core.Transfer(src, req.Paths, dst, req.DestPath, move)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": paths})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.transfer(w, r, sess, true)
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.transfer(w, r, sess, false)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req pathReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	if req.Path != "" {
		req.Paths = append(req.Paths, req.Path)
	}
	if err := s.Core.Delete(a, req.Paths); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req pathReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	p, err := s.Core.Extract(a, req.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": p})
}

func (s *Server) handleImageEdit(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space  string       `json:"space"`
		Path   string       `json:"path"`
		Rotate int          `json:"rotate"`
		Crop   *thumbs.Crop `json:"crop"`
		Copy   bool         `json:"copy"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	fi, err := s.Core.EditImage(a, req.Path, req.Rotate, req.Crop, req.Copy)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fi)
}

func (s *Server) handleStar(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space   string `json:"space"`
		Path    string `json:"path"`
		Starred bool   `json:"starred"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	if err := s.Core.SetStar(a, req.Path, req.Starred); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space string  `json:"space"`
		Path  string  `json:"path"`
		Color *string `json:"color"`
		Note  *string `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	if err := s.Core.SetMeta(a, req.Path, req.Color, req.Note); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleSetTags(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space string   `json:"space"`
		Path  string   `json:"path"`
		Tags  []string `json:"tags"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	if err := s.Core.SetTags(a, req.Path, req.Tags); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	tags, err := s.Core.ListTags(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleStarred(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.Starred(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.Recent(sess.User, int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	q := r.URL.Query()
	list, err := s.Core.Search(sess.User, core.SearchQuery{
		Q:        q.Get("q"),
		Category: q.Get("type"),
		From:     qInt(r, "from"),
		To:       qInt(r, "to"),
		MinSize:  qInt(r, "min_size"),
		MaxSize:  qInt(r, "max_size"),
		Owner:    q.Get("owner"),
		Tag:      q.Get("tag"),
		Space:    q.Get("space"),
		Limit:    int(qInt(r, "limit")),
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	page, err := s.Core.Gallery(sess.User, r.URL.Query().Get("space"), r.URL.Query().Get("cursor"), int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	st, err := s.Core.Storage(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.ListActivity(sess.User, qInt(r, "before"), int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ----- trash -----

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.ListTrash(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type idsReq struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req idsReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.RestoreTrash(sess.User, req.IDs); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req idsReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.PurgeTrash(sess.User, req.IDs); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.EmptyTrash(sess.User); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

// ----- file types -----

func (s *Server) handleFileTypes(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.Core.ListFileTypes())
}

func (s *Server) handleSetFileType(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var t core.FileType
	if err := decode(r, &t); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.SetFileType(t); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleDeleteFileType(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.DeleteFileType(r.PathValue("ext")); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}
