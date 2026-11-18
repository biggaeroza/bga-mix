package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/biggaeroza/bga-mix/internal/app"
	"github.com/biggaeroza/bga-mix/internal/project"
)

func studioServer(t *testing.T) (*Server, *app.Studio) {
	st := app.New()
	t.Cleanup(st.Stop)
	return NewServer(Config{Studio: st, Assets: fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}}), st
}

func body(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestStudio(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	srv, _ := studioServer(t)
	dir := t.TempDir()
	code, v := call(t, srv, "POST", "/api/project/new", "")
	if code != 200 {
		t.Fatal(v)
	}
	var p project.Project
	b, _ := json.Marshal(v["project"])
	json.Unmarshal(b, &p)
	p.Output = project.Output{Width: 320, Height: 180, FPS: 25}
	path := filepath.Join(dir, "show.bgamix")
	if code, v := call(t, srv, "POST", "/api/project/save", body(map[string]any{"path": filepath.Join(dir, "show"), "project": p})); code != 200 || v["path"] != path {
		t.Errorf("save %d %v", code, v)
	}
	if code, v := call(t, srv, "POST", "/api/project/open", body(map[string]any{"path": path})); code != 200 || v["path"] != path {
		t.Errorf("open %d %v", code, v)
	}
	if code, _ := call(t, srv, "POST", "/api/project/open", `{"path": ""}`); code != 400 {
		t.Errorf("open without a path in the browser: %d", code)
	}
	bad := p
	bad.Feeds = append([]project.Feed{}, p.Feeds...)
	bad.Feeds = append(bad.Feeds, project.Feed{ID: "clip", Type: "file", Path: "missing.mp4"})
	if code, v := call(t, srv, "POST", "/api/project/check", body(map[string]any{"path": path, "project": bad})); code != 200 || !strings.Contains(body(v), "feeds[2].path") {
		t.Errorf("check %d %v", code, v)
	}
	if code, v := call(t, srv, "POST", "/api/show/start", body(map[string]any{"path": path, "project": bad})); code != 422 || v["problems"] == nil {
		t.Errorf("start with problems %d %v", code, v)
	}
	if code, _ := call(t, srv, "GET", "/api/status", ""); code != 409 {
		t.Errorf("status with no show: %d", code)
	}
	if code, v := call(t, srv, "POST", "/api/show/start", body(map[string]any{"path": path, "project": p})); code != 200 || v["layout"] != "full" {
		t.Fatalf("start %d %v", code, v)
	}
	if code, _ := call(t, srv, "POST", "/api/layout", `{"layout": "pip"}`); code != 200 {
		t.Errorf("layout %d", code)
	}
	time.Sleep(200 * time.Millisecond)
	code, v = call(t, srv, "GET", "/api/studio?after=0", "")
	if code != 200 || v["running"] != true || len(v["events"].([]any)) < 2 {
		t.Errorf("studio %d %v", code, v)
	}
	seq := int(v["seq"].(float64))
	if _, v := call(t, srv, "GET", "/api/studio?after="+itoa(seq), ""); len(v["events"].([]any)) != 0 {
		t.Errorf("events after %d: %v", seq, v["events"])
	}
	if code, _ := call(t, srv, "POST", "/api/show/stop", ""); code != 200 {
		t.Errorf("stop %d", code)
	}
	if _, v := call(t, srv, "GET", "/api/studio?after="+itoa(seq), ""); v["running"] != false {
		t.Errorf("after stop %v", v)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestFilesAndMedia(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	srv, _ := studioServer(t)
	dir := t.TempDir()
	png := filepath.Join(dir, "card.png")
	if err := exec(png); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "x.txt"), nil, 0o644)
	code, v := call(t, srv, "GET", "/api/files?dir="+dir, "")
	if code != 200 || len(v["entries"].([]any)) != 1 {
		t.Errorf("files %d %v", code, v)
	}
	if code, v := call(t, srv, "GET", "/api/media?path="+png, ""); code != 200 || v["ok"] != true || v["kind"] != "image" {
		t.Errorf("media %d %v", code, v)
	}
	req := httptest.NewRequest("GET", "/api/media/thumb?path="+png, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumb %d", rec.Code)
	}
	if code, _ := call(t, srv, "GET", "/api/media?path="+dir+"/nope.png", ""); code != 404 {
		t.Errorf("missing media %d", code)
	}
	if code, _ := call(t, srv, "POST", "/api/browse", `{"kind": "media"}`); code != 400 {
		t.Errorf("browse in the browser %d", code)
	}
	if code, v := call(t, srv, "GET", "/api/meta", ""); code != 200 || v["native_dialogs"] != false || len(v["patterns"].([]any)) == 0 {
		t.Errorf("meta %d %v", code, v)
	}
	if code, _ := call(t, srv, "GET", "/", ""); code != 200 {
		t.Errorf("the UI %d", code)
	}
}

// The browser shell's guard: the Host and Origin must be the server's, and the token is needed.
func TestGuard(t *testing.T) {
	st := app.New()
	srv := NewServer(Config{Studio: st, Token: "tok", AllowedHosts: []string{"127.0.0.1:8740"}})
	for _, c := range []struct {
		host, origin, token string
		code                int
	}{
		{"127.0.0.1:8740", "", "tok", 200},
		{"evil.example:8740", "", "tok", 403},
		{"127.0.0.1:8740", "http://evil.example", "tok", 403},
		{"127.0.0.1:8740", "http://127.0.0.1:8740", "tok", 200},
		{"127.0.0.1:8740", "", "", 401},
	} {
		req := httptest.NewRequest("GET", "/api/meta", nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.token != "" {
			req.Header.Set("X-BGAMix-Token", c.token)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != c.code {
			t.Errorf("%+v: %d", c, rec.Code)
		}
	}
	_ = http.StatusOK
}

func exec(png string) error {
	return runGst("videotestsrc num-buffers=1 pattern=red ! video/x-raw,width=64,height=36 ! pngenc ! filesink location=" + png)
}

func TestDevicesAndAudioAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	srv, st := studioServer(t)
	code, v := call(t, srv, "GET", "/api/devices", "")
	if code != 200 || v["cameras"] == nil || v["audio_inputs"] == nil {
		t.Errorf("devices %d %v", code, v)
	}
	t.Logf("devices %v", v)
	p := app.NewProject()
	p.Output = project.Output{Width: 320, Height: 180, FPS: 25}
	p.Feeds = append(p.Feeds, project.Feed{ID: "tone", Type: "audio", Pattern: "tone"})
	if err := st.Start(p, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if code, v := call(t, srv, "POST", "/api/feeds/tone/audio", `{"volume_db": -6, "mode": "on"}`); code != 200 {
		t.Errorf("audio %d %v", code, v)
	}
	if code, _ := call(t, srv, "POST", "/api/feeds/bars/audio", `{"mute": true}`); code != 400 {
		t.Errorf("a feed without sound: %d", code)
	}
	if code, _ := call(t, srv, "POST", "/api/feeds/tone/audio", `{"volume_db": 40}`); code != 400 {
		t.Errorf("+40 dB: %d", code)
	}
	time.Sleep(400 * time.Millisecond)
	code, v = call(t, srv, "GET", "/api/levels", "")
	if code != 200 || v["program"] == nil || v["tone"] == nil {
		t.Errorf("levels %d %v", code, v)
	}
	if code, _ := call(t, srv, "POST", "/api/monitor/radio", `{"on": true}`); code != 400 {
		t.Errorf("unknown monitor %d", code)
	}
	if code, v := call(t, srv, "GET", "/api/meta", ""); code != 200 || len(v["encoders"].([]any)) != 5 {
		t.Errorf("meta %v", v)
	}
}
