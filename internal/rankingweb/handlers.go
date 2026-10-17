// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2011-2017 Luca Wehrstedt and the CMS contributors
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmsranking/RankingWebServer.py of CMS (http://cms-dev.github.io/).

package rankingweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// realm is the realm of the basic authentication (CMS realm_name).
const realm = "Scoreboard"

func (s *Server) mount(mux interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
	Handle(string, http.Handler)
}) {
	mux.HandleFunc("GET /{$}", s.root)
	mux.HandleFunc("GET /config", s.publicConfig)
	mux.HandleFunc("GET /scores", s.scores)
	mux.HandleFunc("GET /history", s.history)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /sublist/{user}", s.sublist)
	mux.HandleFunc("GET /logo", func(w http.ResponseWriter, r *http.Request) {
		s.image(w, r, filepath.Join(s.store.dir, "logo"), "img/logo.png")
	})
	mux.HandleFunc("GET /faces/{name}", func(w http.ResponseWriter, r *http.Request) {
		s.namedImage(w, r, "faces", "img/face.png")
	})
	mux.HandleFunc("GET /flags/{name}", func(w http.ResponseWriter, r *http.Request) {
		s.namedImage(w, r, "flags", "img/flag.png")
	})
	for k := range numKinds {
		p := "/" + kinds[k].path + "/"
		mux.HandleFunc("GET "+p+"{$}", func(w http.ResponseWriter, r *http.Request) { s.getList(w, r, k) })
		mux.HandleFunc("GET "+p+"{key}", func(w http.ResponseWriter, r *http.Request) { s.get(w, r, k) })
		mux.HandleFunc("PUT "+p+"{$}", func(w http.ResponseWriter, r *http.Request) { s.putList(w, r, k) })
		mux.HandleFunc("PUT "+p+"{key}", func(w http.ResponseWriter, r *http.Request) { s.put(w, r, k) })
		mux.HandleFunc("DELETE "+p+"{$}", func(w http.ResponseWriter, r *http.Request) { s.deleteList(w, r, k) })
		mux.HandleFunc("DELETE "+p+"{key}", func(w http.ResponseWriter, r *http.Request) { s.delete(w, r, k) })
	}
	// The JavaScript, styles and images of cmsranking.
	files := http.FileServerFS(s.static)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
}

// read checks that the request may read the scoreboard and returns
// whether it sees it as an administrator.
func (s *Server) read(w http.ResponseWriter, r *http.Request) (isAdmin, ok bool) {
	switch s.access(r) {
	case admin:
		return true, true
	case viewer:
		return false, true
	}
	forbidden(w)
	return false, false
}

// timestamp returns the Timestamp header of a response: the time of its
// data in seconds, at least the id of the last event (microseconds), so
// that the JavaScript ignores the events already in the data. The caller
// holds the read lock of the store.
func (s *Server) timestamp() string {
	us := uint64(time.Now().UnixMicro())
	if id, ok := live.ParseID(s.hub.LastID()); ok && id > us {
		us = id
	}
	return fmt.Sprintf("%d.%06d", us/1e6, us%1e6)
}

func writeJSONBody(w http.ResponseWriter, body []byte, ts string) {
	h := w.Header()
	if ts != "" {
		h.Set("Timestamp", ts)
	}
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// cachedBody returns the response body named key for the current version
// of the store, building it once per version (every page load asks for
// the same lists, scores and history). The caller holds the read lock.
func (s *Server) cachedBody(key string, build func() []byte) []byte {
	v := s.store.version.Load()
	s.cacheMu.Lock()
	c, ok := s.cache[key]
	s.cacheMu.Unlock()
	if ok && c.version == v {
		return c.body
	}
	body := build()
	s.cacheMu.Lock()
	s.cache[key] = cached{v, body}
	s.cacheMu.Unlock()
	return body
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if s.grant(w, r) {
		return
	}
	if _, ok := s.read(w, r); !ok {
		return
	}
	f, err := s.static.Open("Ranking.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.Copy(w, f)
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.read(w, r); !ok {
		return
	}
	writeJSONBody(w, mustJSON(map[string]any{"show_id_column": s.cfg.RankingWeb.ShowIDColumn}), "")
}

func viewKey(name string, isAdmin bool) string {
	if isAdmin {
		return name + "/admin"
	}
	return name
}

func (s *Server) scores(w http.ResponseWriter, r *http.Request) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()
	body := s.cachedBody(viewKey("scores", isAdmin), func() []byte {
		return mustJSON(st.scoring(isAdmin).scoreMap())
	})
	writeJSONBody(w, body, s.timestamp())
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()
	body := s.cachedBody(viewKey("history", isAdmin), func() []byte {
		return mustJSON(st.scoring(isAdmin).globalHistory())
	})
	writeJSONBody(w, body, "")
}

func (s *Server) sublist(w http.ResponseWriter, r *http.Request) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	st := s.store
	st.mu.RLock()
	body := mustJSON(st.scoring(isAdmin).sublist(r.PathValue("user"), st.visibleSubmission(isAdmin)))
	st.mu.RUnlock()
	writeJSONBody(w, body, "")
}

// events streams the changes (CMS DataWatcher): "contest", "task", "team"
// and "user" events with "create|update|delete <key>", "score" events
// with "<user> <task> <score>", and "reload" when events were lost.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	scores := topicPublic
	if isAdmin {
		scores = topicFull
	}
	s.hub.Stream(w, r, live.StreamOptions{Topics: []live.Topic{topicAll, scores}, ResyncEvent: "reload"})
}

// readable reports whether a kind may be read: submissions and
// subchanges reveal the scores hidden by a freeze, so only administrators
// read them (the page of the scoreboard never does).
func readable(k kind, isAdmin bool) bool {
	return isAdmin || (k != kSubmission && k != kSubchange)
}

func (s *Server) getList(w http.ResponseWriter, r *http.Request, k kind) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	if !readable(k, isAdmin) {
		forbidden(w)
		return
	}
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()
	body := s.cachedBody("list/"+kinds[k].path, func() []byte { return st.list(k) })
	writeJSONBody(w, body, s.timestamp())
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, k kind) {
	isAdmin, ok := s.read(w, r)
	if !ok {
		return
	}
	if !readable(k, isAdmin) {
		forbidden(w)
		return
	}
	key := r.PathValue("key")
	if !ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()
	body, found := st.get(k, key)
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSONBody(w, body, s.timestamp())
}

// authorize checks the credentials of a write.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if s.basicOK(r) {
		return true
	}
	s.rt.Log.Warn("unauthorized request", "path", r.URL.Path, "client", r.RemoteAddr)
	w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	return false
}

// body reads the JSON body of a write.
func (s *Server) body(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		s.rt.Log.Warn("unsupported MIME type", "path", r.URL.Path, "type", mt)
		http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
		return nil, false
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return nil, false
	}
	if !json.Valid(b) {
		s.rt.Log.Warn("wrong JSON", "path", r.URL.Path)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return nil, false
	}
	return b, true
}

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrInvalidData):
		s.rt.Log.Warn("invalid data", "path", r.URL.Path, "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	case errors.Is(err, errNotFound):
		http.NotFound(w, r)
	default:
		s.rt.Log.Error("ranking write failed", "path", r.URL.Path, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, k kind) {
	key := r.PathValue("key")
	if !ValidKey(key) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	b, ok := s.body(w, r)
	if !ok {
		return
	}
	s.writeResult(w, r, s.store.Put(k, key, b))
}

func (s *Server) putList(w http.ResponseWriter, r *http.Request, k kind) {
	if !s.authorize(w, r) {
		return
	}
	b, ok := s.body(w, r)
	if !ok {
		return
	}
	var data map[string]json.RawMessage
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' || json.Unmarshal(t, &data) != nil {
		s.writeResult(w, r, invalid("Not a dictionary"))
		return
	}
	s.writeResult(w, r, s.store.Merge(k, data))
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request, k kind) {
	key := r.PathValue("key")
	if !ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	s.writeResult(w, r, s.store.Delete(k, key))
}

func (s *Server) deleteList(w http.ResponseWriter, r *http.Request, k kind) {
	if !s.authorize(w, r) {
		return
	}
	s.writeResult(w, r, s.store.DeleteAll(k))
}

// imageTypes are the extensions CMS looks for, in order.
var imageTypes = []struct{ ext, mime string }{
	{"png", "image/png"}, {"jpg", "image/jpeg"}, {"gif", "image/gif"}, {"bmp", "image/bmp"},
}

func (s *Server) namedImage(w http.ResponseWriter, r *http.Request, dir, fallback string) {
	name := r.PathValue("name")
	if !ValidKey(name) {
		http.NotFound(w, r)
		return
	}
	s.image(w, r, filepath.Join(s.store.dir, dir, name), fallback)
}

// image serves base.<ext> from data_dir, or the default image of
// cmsranking (CMS ImageHandler).
func (s *Server) image(w http.ResponseWriter, r *http.Request, base, fallback string) {
	if _, ok := s.read(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	for _, t := range imageTypes {
		f, err := os.Open(base + "." + t.ext)
		if err != nil {
			continue
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			continue
		}
		w.Header().Set("Content-Type", t.mime)
		http.ServeContent(w, r, "", fi.ModTime(), f)
		return
	}
	data, err := fs.ReadFile(s.static, fallback)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
