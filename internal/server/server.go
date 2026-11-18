// Package server is BGAMix's control API, for a running show:
//
//	GET  /api/status                         the show's state (JSON)
//	POST /api/layout                         {"layout": "pip", "transition": {"kind": "mix", "duration_s": 1}}
//	POST /api/feeds/{id}/{command}           play, pause, restart, seek ({"seconds": 12.5}), next, previous
//	POST /api/recordings/{label}/{command}   start, stop, pause, resume ("program-1" …, "iso-<feed>")
//	GET  /api/preview/{key}                  the latest JPEG of "program" or a feed
//	GET  /api/preview/{key}/stream           the same as an MJPEG stream (browsers, VLC)
//	GET  /api/ws                             WebSocket: {"type": "status", "status": …} at once and
//	                                         every second, {"type": "event", "event": …} as they happen
//
// Errors are {"error": "…"} with status 400 (a bad request), 404 (nothing by that name) or 401.
// With a token, every request needs it: "Authorization: Bearer <token>" or "?token=<token>".
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
)

// statusEvery is how often WebSocket clients get the status.
const statusEvery = time.Second

// Server serves the control API of one show.
type Server struct {
	show  *engine.Show
	token string
	mux   *http.ServeMux
}

// New makes the API for a running show; token "" means no token is asked for.
func New(show *engine.Show, token string) *Server {
	s := &Server{show: show, token: token, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, show.Status()) })
	s.mux.HandleFunc("POST /api/layout", s.layout)
	s.mux.HandleFunc("POST /api/feeds/{id}/{cmd}", s.feed)
	s.mux.HandleFunc("POST /api/recordings/{label}/{cmd}", s.recording)
	s.mux.HandleFunc("GET /api/preview/{key}", s.preview)
	s.mux.HandleFunc("GET /api/preview/{key}/stream", s.stream)
	s.mux.HandleFunc("GET /api/ws", s.ws)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token != "" {
		got := r.URL.Query().Get("token")
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, 401, errors.New("a token is needed"))
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// readBody decodes an optional JSON body strictly.
func readBody(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil && err != io.EOF {
		return fmt.Errorf("the request body: %v", err)
	}
	return nil
}

// commandErr maps an engine error: unknown names are 404, the rest 400.
func commandErr(w http.ResponseWriter, err error) {
	code := 400
	if strings.HasPrefix(err.Error(), "no ") {
		code = 404
	}
	writeErr(w, code, err)
}

func (s *Server) layout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Layout     string              `json:"layout"`
		Transition *project.Transition `json:"transition"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if t := req.Transition; t != nil && ((t.Kind != project.TransitionCut && t.Kind != project.TransitionMix) || t.DurationS < 0 || t.DurationS > 60) {
		writeErr(w, 400, errors.New("transition: kind cut or mix, duration_s 0 to 60"))
		return
	}
	if err := s.show.SetLayout(req.Layout, req.Transition); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, s.show.Status())
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds float64 `json:"seconds"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.show.FeedCommand(r.PathValue("id"), r.PathValue("cmd"), req.Seconds); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, s.show.Status())
}

func (s *Server) recording(w http.ResponseWriter, r *http.Request) {
	if err := s.show.RecordingCommand(r.PathValue("label"), r.PathValue("cmd")); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, s.show.Status())
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.show.Preview(r.PathValue("key"))
	if !ok {
		writeErr(w, 404, fmt.Errorf("no preview %q (yet)", r.PathValue("key")))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

// stream sends the preview as multipart MJPEG, a frame whenever a new one is there.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, _, ok := s.show.Preview(key); !ok {
		if st := s.show.Status(); !st.Previews {
			writeErr(w, 404, errors.New("previews are off"))
			return
		}
	}
	const boundary = "bgamixframe"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store")
	fl, _ := w.(http.Flusher)
	var last time.Time
	tick := time.NewTicker(time.Second / (2 * engine.PreviewFPS))
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		b, at, ok := s.show.Preview(key)
		if !ok || !at.After(last) {
			continue
		}
		last = at
		if _, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(b)); err != nil {
			return
		}
		w.Write(b)
		io.WriteString(w, "\r\n")
		if fl != nil {
			fl.Flush()
		}
	}
}

// Message is what the WebSocket sends.
type Message struct {
	Type   string         `json:"type"` // status, event
	Status *engine.Status `json:"status,omitempty"`
	Event  *engine.Event  `json:"event,omitempty"`
}

var upgrader = websocket.Upgrader{} // same-origin browsers only (clients without an Origin header are let in)

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	events, cancel := s.show.Subscribe()
	defer cancel()
	gone := make(chan struct{})
	go func() { // reads only to notice the client closing
		defer close(gone)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	send := func(m Message) bool {
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return c.WriteJSON(m) == nil
	}
	status := func() bool { st := s.show.Status(); return send(Message{Type: "status", Status: &st}) }
	if !status() {
		return
	}
	tick := time.NewTicker(statusEvery)
	defer tick.Stop()
	for {
		select {
		case <-gone:
			return
		case e := <-events:
			if !send(Message{Type: "event", Event: &e}) {
				return
			}
		case <-tick.C:
			if !status() {
				return
			}
		}
	}
}
