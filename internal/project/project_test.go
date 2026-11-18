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
		{`"fps": 30`, `"fps": 0`}:                                   "output.fps",
		{`"width": 1280`, `"width": 1281`}:                          "output.width",
		{`"type": "test"`, `"type": "camera"`}:                      "feeds[0].type",
		{`"id": "clip"`, `"id": "cam"`}:                             "feeds[1].id",
		{`"end": "hold"`, `"end": "rewind"`}:                        "feeds[1].end",
		{`"feed": "clip"`, `"feed": "nope"`}:                        "layouts[0].boxes[1].feed",
		{`"w": 0.35`, `"w": 1.5`}:                                   "layouts[0].boxes[1]",
		{`"path": "rec/{date}"`, `"path": "r", "container": "avi"`}: "recordings[0].container",
		{`"name": "t"`, `"name": "t", "colour": "red"`}:             "unknown field",
		{`"feeds": [`, `"start_layout": "x", "feeds": [`}:           "start_layout",
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
