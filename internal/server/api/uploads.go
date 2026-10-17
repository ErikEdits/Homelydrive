package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/erikedits/homelydrive/internal/server/core"
)

// Upload protocol (resumable, any file size):
//
//	POST  /api/v1/uploads        {space, path, size, sha256?, mtime?, conflict?}
//	      -> {id, status:"pending", offset:0} or {status:"exists"|"copied"|"skipped"|"done"}
//	PATCH /api/v1/uploads/{id}   Upload-Offset: <n>, body = bytes from n on
//	      -> {status:"pending", offset} or {status:"done", file}
//	HEAD  /api/v1/uploads/{id}   -> Upload-Offset header (to resume)
//	DELETE /api/v1/uploads/{id}  cancel

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req core.UploadRequest
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.Core.CreateUpload(sess.User, req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func writeOffset(w http.ResponseWriter, st *core.UploadStatus) {
	w.Header().Set("Upload-Offset", strconv.FormatInt(st.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(st.Size, 10))
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUploadOffset(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	st, err := s.Core.UploadOffset(core.UserOwner(sess.User), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeOffset(w, st)
}

func uploadOffsetHeader(r *http.Request) (int64, error) {
	off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || off < 0 {
		return 0, &core.BadRequest{Msg: "Upload-Offset fehlt."}
	}
	return off, nil
}

func (s *Server) chunk(w http.ResponseWriter, r *http.Request, owner any) {
	off, err := uploadOffsetHeader(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var st *core.UploadStatus
	switch o := owner.(type) {
	case *core.User:
		st, err = s.Core.WriteUpload(core.UserOwner(o), r.PathValue("id"), off, r.Body)
	case *core.Link:
		st, err = s.Core.WriteUpload(core.LinkOwner(o), r.PathValue("id"), off, r.Body)
	}
	if err != nil {
		var om *core.ErrOffsetMismatch
		if st != nil && !errors.As(err, &om) {
			// The connection broke mid-chunk; report what we have so the
			// client can resume (it will most likely not read this).
			writeOffset(w, st)
			return
		}
		s.fail(w, err)
		return
	}
	writeOffset(w, st)
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.chunk(w, r, sess.User)
}

func (s *Server) handleCancelUpload(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.CancelUpload(core.UserOwner(sess.User), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}
