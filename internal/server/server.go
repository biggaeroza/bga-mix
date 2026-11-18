// Package server is BGAMix's control API, for a running show:
//
//	GET  /api/status                         the show's state (JSON)
//	POST /api/layout                         {"layout": "pip", "transition": {"kind": "mix", "duration_s": 1}}
//	POST /api/feeds/{id}/{command}           play, pause, restart, seek ({"seconds": 12.5}), next, previous
//	POST /api/feeds/{id}/audio               {"volume_db": -6, "mute": false, "mode": "follow|on|off"} (any of them)
//	POST /api/recordings/{label}/{command}   start, stop, pause, resume ("program-1" …, "iso-<feed>", "stream-1" …)
//	POST /api/monitor/{window|listen}        {"on": true}: the program in a window / its sound on the speakers
//	GET  /api/levels                         the meters: {"program": {"peak_db", "rms_db"}, "<feed>": …}
//	GET  /api/preview/{key}                  the latest JPEG of "program" or a feed
//	GET  /api/preview/{key}/stream           the same as an MJPEG stream (browsers, VLC)
//	GET  /api/ws                             WebSocket: {"type": "status", "status": …} at once and
//	                                         every second, {"type": "event", "event": …} as they happen
//
// Errors are {"error": "…"} with status 400 (a bad request), 404 (nothing by that name), 409 (no
// show is running) or 401. With a token, every API request needs it: "Authorization: Bearer
// <token>", "X-BGAMix-Token: <token>" or "?token=<token>" (images and WebSockets cannot send
// headers).
//
// With a studio (Config.Studio), the same handler also serves the GUI: the endpoints in
// studio.go and the embedded web UI. The access guard checks the allowed hosts (against DNS
// rebinding) and the token.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/biggaeroza/bga-mix/internal/app"
	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
)

// statusEvery is how often WebSocket clients get the status.
const statusEvery = time.Second

// Dialogs are the desktop's native file dialogs (nil in the browser). kind is "show", "media",
// "image" or "program". An empty path with a nil error means the user cancelled.
type Dialogs interface {
	OpenFile(kind string) (string, error)
	SaveFile(kind, suggested string) (string, error)
	OpenFolder(title string) (string, error)
}

// Config sets up a server.
type Config struct {
	// Show is the show the control API acts on (nil: none is running).
	Show func() *engine.Show
	// Token, if set, is needed on every API request.
	Token string
	// AllowedHosts, if set, are the only Host (and Origin) headers accepted.
	AllowedHosts []string
	// Studio serves the GUI's endpoints, with Assets the UI, Dialogs the native dialogs (desktop)
	// and InitialPath a show to open first.
	Studio      *app.Studio
	Assets      fs.FS
	Dialogs     Dialogs
	InitialPath string
	Log         io.Writer
}

// Server serves the control API (and with a studio, the GUI).
type Server struct {
	cfg Config
	mux *http.ServeMux
}

// New makes the control API for one running show; token "" means no token is asked for.
func New(show *engine.Show, token string) *Server {
	return NewServer(Config{Show: func() *engine.Show { return show }, Token: token})
}

// NewServer makes a server.
func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/status", s.withShow(func(sh *engine.Show, w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, sh.Status()) }))
	s.mux.HandleFunc("POST /api/layout", s.withShow(s.layout))
	s.mux.HandleFunc("POST /api/feeds/{id}/{cmd}", s.withShow(s.feed))
	s.mux.HandleFunc("POST /api/feeds/{id}/audio", s.withShow(s.audio))
	s.mux.HandleFunc("POST /api/monitor/{kind}", s.withShow(s.monitor))
	s.mux.HandleFunc("GET /api/levels", s.withShow(func(sh *engine.Show, w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, sh.Levels()) }))
	s.mux.HandleFunc("POST /api/recordings/{label}/{cmd}", s.withShow(s.recording))
	s.mux.HandleFunc("GET /api/preview/{key}", s.withShow(s.preview))
	s.mux.HandleFunc("GET /api/preview/{key}/stream", s.withShow(s.stream))
	s.mux.HandleFunc("GET /api/ws", s.withShow(s.ws))
	if cfg.Studio != nil {
		s.studioRoutes()
	}
	if cfg.Assets != nil {
		s.mux.Handle("GET /", http.FileServer(http.FS(cfg.Assets)))
	}
	return s
}

// withShow runs a handler on the running show, or answers 409.
func (s *Server) withShow(f func(*engine.Show, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sh *engine.Show
		if s.cfg.Show != nil {
			sh = s.cfg.Show()
		}
		if sh == nil {
			writeErr(w, 409, errors.New("no show is running"))
			return
		}
		f(sh, w, r)
	}
}

func (s *Server) hostAllowed(host string) bool {
	for _, a := range s.cfg.AllowedHosts {
		if strings.EqualFold(host, a) {
			return true
		}
	}
	return false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.AllowedHosts) > 0 {
		if !s.hostAllowed(r.Host) {
			writeErr(w, 403, errors.New("host not allowed"))
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.hostAllowed(strings.TrimPrefix(o, "http://")) {
			writeErr(w, 403, errors.New("origin not allowed"))
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if s.cfg.Token != "" {
			got := r.URL.Query().Get("token")
			if h := r.Header.Get("X-BGAMix-Token"); h != "" {
				got = h
			}
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				got = strings.TrimPrefix(h, "Bearer ")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
				writeErr(w, 401, errors.New("a token is needed"))
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
	}
	if s.cfg.Log == nil {
		s.mux.ServeHTTP(w, r)
		return
	}
	t0 := time.Now()
	s.mux.ServeHTTP(w, r)
	fmt.Fprintf(s.cfg.Log, "%s %s %s\n", r.Method, r.URL.Path, time.Since(t0).Round(time.Millisecond))
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
func readBody(r *http.Request, v any) error { return readBodyLimit(r, v, 1<<16) }

func readBodyLimit(r *http.Request, v any, limit int64) error {
	d := json.NewDecoder(io.LimitReader(r.Body, limit))
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

func (s *Server) layout(show *engine.Show, w http.ResponseWriter, r *http.Request) {
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
	if err := show.SetLayout(req.Layout, req.Transition); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, show.Status())
}

func (s *Server) feed(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds float64 `json:"seconds"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := show.FeedCommand(r.PathValue("id"), r.PathValue("cmd"), req.Seconds); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, show.Status())
}

func (s *Server) audio(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	var req struct {
		VolumeDB *float64 `json:"volume_db"`
		Mute     *bool    `json:"mute"`
		Mode     string   `json:"mode"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := show.AudioCommand(r.PathValue("id"), req.VolumeDB, req.Mute, req.Mode); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, show.Status())
}

func (s *Server) monitor(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := show.Monitor(r.PathValue("kind"), req.On); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, show.Status())
}

func (s *Server) recording(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	if err := show.RecordingCommand(r.PathValue("label"), r.PathValue("cmd")); err != nil {
		commandErr(w, err)
		return
	}
	writeJSON(w, 200, show.Status())
}

func (s *Server) preview(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	b, _, ok := show.Preview(r.PathValue("key"))
	if !ok {
		writeErr(w, 404, fmt.Errorf("no preview %q (yet)", r.PathValue("key")))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

// stream sends the preview as multipart MJPEG, a frame whenever a new one is there.
func (s *Server) stream(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, _, ok := show.Preview(key); !ok {
		if st := show.Status(); !st.Previews {
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
		b, at, ok := show.Preview(key)
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

func (s *Server) ws(show *engine.Show, w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	events, cancel := show.Subscribe()
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
	status := func() bool { st := show.Status(); return send(Message{Type: "status", Status: &st}) }
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
