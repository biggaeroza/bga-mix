package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
)

func startShow(t *testing.T) *engine.Show {
	t.Helper()
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	p := &project.Project{FormatVersion: 1, Name: "api", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "red", Type: "test", Pattern: "red"}, {ID: "green", Type: "test", Pattern: "green"}},
		Layouts: []project.Layout{{ID: "red", Boxes: []project.Box{{Feed: "red", W: 1, H: 1}}},
			{ID: "green", Boxes: []project.Box{{Feed: "green", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: t.TempDir() + "/r.mkv", Container: "mkv", Manual: true}},
	}
	s, err := engine.Start(p, "", engine.Options{Previews: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var v map[string]any
	json.Unmarshal(rec.Body.Bytes(), &v)
	return rec.Code, v
}

func TestCommands(t *testing.T) {
	srv := New(startShow(t), "")
	if code, v := call(t, srv, "GET", "/api/status", ""); code != 200 || v["layout"] != "red" || len(v["feeds"].([]any)) != 2 {
		t.Errorf("status %d %v", code, v)
	}
	if code, v := call(t, srv, "POST", "/api/layout", `{"layout": "green", "transition": {"kind": "cut"}}`); code != 200 || v["layout"] != "green" {
		t.Errorf("layout %d %v", code, v)
	}
	for _, c := range []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/layout", `{"layout": "nope"}`, 404},
		{"POST", "/api/layout", `{"layout": "red", "transition": {"kind": "wipe"}}`, 400},
		{"POST", "/api/layout", `{"layot": "red"}`, 400},
		{"POST", "/api/feeds/red/pause", "", 400}, // a test pattern does not pause
		{"POST", "/api/feeds/nope/play", "", 404},
		{"POST", "/api/feeds/red/fly", "", 400},
		{"POST", "/api/recordings/program-1/start", "", 200},
		{"POST", "/api/recordings/program-1/start", "", 400}, // already
		{"POST", "/api/recordings/program-9/start", "", 404},
		{"GET", "/api/layout", "", 405},
	} {
		if code, v := call(t, srv, c.method, c.path, c.body); code != c.code {
			t.Errorf("%s %s %s: %d %v, want %d", c.method, c.path, c.body, code, v, c.code)
		}
	}
	time.Sleep(time.Second)
	if code, v := call(t, srv, "POST", "/api/recordings/program-1/stop", ""); code != 200 {
		t.Errorf("stop %d %v", code, v)
	}
}

func TestToken(t *testing.T) {
	srv := New(startShow(t), "s3cret")
	if code, _ := call(t, srv, "GET", "/api/status", ""); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := call(t, srv, "GET", "/api/status?token=s3cret", ""); code != 200 {
		t.Errorf("query token: %d", code)
	}
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("bearer: %d", rec.Code)
	}
}

func TestPreviewAndStream(t *testing.T) {
	ts := httptest.NewServer(New(startShow(t), ""))
	defer ts.Close()
	time.Sleep(1500 * time.Millisecond)
	resp, err := http.Get(ts.URL + "/api/preview/program")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("preview: %v %s", err, resp.Header.Get("Content-Type"))
	}
	if r, _ := http.Get(ts.URL + "/api/preview/nope"); r.StatusCode != 404 {
		t.Errorf("unknown preview %d", r.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/api/preview/red/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	frames := 0
	deadline := time.Now().Add(3 * time.Second)
	for frames < 3 && time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "--bgamixframe") {
			frames++
		}
	}
	if frames < 3 {
		t.Errorf("%d MJPEG frames", frames)
	}
}

func TestWebSocket(t *testing.T) {
	show := startShow(t)
	ts := httptest.NewServer(New(show, ""))
	defer ts.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var m Message
	if err := c.ReadJSON(&m); err != nil || m.Type != "status" || m.Status.Layout != "red" {
		t.Fatalf("first message %+v %v", m, err)
	}
	show.SetLayout("green", nil)
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var m Message
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("no layout event: %v", err)
		}
		if m.Type == "event" && m.Event.Kind == "layout" && strings.HasPrefix(m.Event.Text, "green") {
			break
		}
	}
}
