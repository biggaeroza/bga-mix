package project

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `{
  "format_version": 1, "name": "t",
  "output": {"width": 1280, "height": 720, "fps": 30},
  "feeds": [{"id": "cam", "type": "test"}, {"id": "clip", "type": "file", "path": "a.mp4", "end": "hold"}],
  "layouts": [{"id": "pip", "boxes": [{"feed": "cam", "x": 0, "y": 0, "w": 1, "h": 1}, {"feed": "clip", "x": 0.6, "y": 0.05, "w": 0.35, "h": 0.35}]}],
  "recordings": [{"path": "rec/{date}"}]
}`

func TestReadValid(t *testing.T) {
	p, err := Read(strings.NewReader(valid), "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Start().ID != "pip" || len(p.Feeds) != 2 {
		t.Errorf("%+v", p)
	}
	var b bytes.Buffer
	p.Write(&b)
	if _, err := Read(&b, ""); err != nil {
		t.Errorf("round trip: %v", err)
	}
}

// Problems are reported with their paths; unknown fields are refused.
func TestProblems(t *testing.T) {
	for edit, want := range map[[2]string]string{
		{`"fps": 30`, `"fps": 0`}:                                                                      "output.fps",
		{`"width": 1280`, `"width": 1281`}:                                                             "output.width",
		{`"type": "test"`, `"type": "hologram"`}:                                                       "feeds[0].type",
		{`"id": "clip"`, `"id": "cam"`}:                                                                "feeds[1].id",
		{`"end": "hold"`, `"end": "rewind"`}:                                                           "feeds[1].end",
		{`"feed": "clip"`, `"feed": "nope"`}:                                                           "layouts[0].boxes[1].feed",
		{`"w": 0.35`, `"w": 1.5`}:                                                                      "layouts[0].boxes[1]",
		{`"path": "rec/{date}"`, `"path": "r", "container": "avi"`}:                                    "recordings[0].container",
		{`"name": "t"`, `"name": "t", "colour": "red"`}:                                                "unknown field",
		{`"feeds": [`, `"start_layout": "x", "feeds": [`}:                                              "start_layout",
		{`"feeds": [`, `"transition": {"kind": "wipe"}, "feeds": [`}:                                   "transition.kind",
		{`"type": "test"`, `"type": "stream", "url": "ftp://x"`}:                                       "feeds[0].url",
		{`"type": "test"`, `"type": "audio"`}:                                                          "layouts[0].boxes[0].feed",
		{`"end": "hold"`, `"end": "hold", "audio": "loud"`}:                                            "feeds[1].audio",
		{`"end": "hold"`, `"end": "hold", "volume_db": 40`}:                                            "feeds[1].volume_db",
		{`"recordings": [`, `"streams": [{"url": "rtmp://x/live", "codec": "h265"}], "recordings": [`}: "streams[0].codec",
		{`"recordings": [`, `"streams": [{"url": "rtmp:///live"}], "recordings": [`}:                   "streams[0].url",
		{`"path": "rec/{date}"`, `"path": "r", "encoder": "gpu"`}:                                      "recordings[0].encoder",
		{`"type": "test"`, `"type": "playlist"`}:                                                       "feeds[0].items",
		{`"end": "hold"`, `"end": "return", "iso": {"path": ""}`}:                                      "feeds[1].iso.path",
		{`"path": "rec/{date}"`, `"path": "r", "segment_s": 1`}:                                        "recordings[0].segment_s",
	} {
		_, err := Read(strings.NewReader(strings.Replace(valid, edit[0], edit[1], 1)), "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s → %s: %v", edit[0], edit[1], err)
		}
	}
}

// With the project's folder, missing files are reported.
func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(strings.NewReader(valid), dir); err == nil || !strings.Contains(err.Error(), "feeds[1].path") {
		t.Errorf("missing file: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "a.mp4"), []byte("x"), 0o644)
	if _, err := Read(strings.NewReader(valid), dir); err != nil {
		t.Error(err)
	}
}

func TestDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := VideosDir(); got != filepath.Join(home, "Videos") {
		t.Errorf("no user-dirs: %s", got)
	}
	os.MkdirAll(filepath.Join(home, ".config"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "user-dirs.dirs"), []byte("# x\nXDG_VIDEOS_DIR=\"$HOME/Vidéos\"\n"), 0o644)
	if got := VideosDir(); got != filepath.Join(home, "Vidéos") {
		t.Errorf("user-dirs: %s", got)
	}
	if got := UnsavedRecordingsDir(); got != filepath.Join(home, "Vidéos", "BGAMix") {
		t.Error(got)
	}
	if got := Resolve("/show", "~/rec/a.mp4"); got != filepath.Join(home, "rec", "a.mp4") {
		t.Errorf("~: %s", got)
	}
	if got := Resolve("/show", "rec/a.mp4"); got != "/show/rec/a.mp4" {
		t.Error(got)
	}
}
