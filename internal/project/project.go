// Package project is the .bgamix file: a show as versioned JSON (format_version 1): the output
// picture, the feeds, the layouts that place them, and the recordings. Files are decoded strictly
// (unknown fields are errors) and validated with the path of every problem.
//
// Layout boxes are given as fractions of the output picture (0 to 1), so a project does not
// depend on the output resolution.
package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FormatVersion is the format this package reads and writes.
const FormatVersion = 1

// Project is a .bgamix file.
type Project struct {
	FormatVersion int         `json:"format_version"`
	Name          string      `json:"name"`
	Notes         string      `json:"notes,omitempty"`
	Output        Output      `json:"output"`
	Feeds         []Feed      `json:"feeds"`
	Layouts       []Layout    `json:"layouts"`
	StartLayout   string      `json:"start_layout,omitempty"` // default the first layout
	Recordings    []Recording `json:"recordings,omitempty"`
}

// Output is the mixed picture.
type Output struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`
}

// Feed types.
const (
	FeedTest  = "test"  // a generated test pattern
	FeedFile  = "file"  // a video file
	FeedImage = "image" // a still image
)

// End behaviours of a file feed when it reaches its end (or its out point).
const (
	EndLoop  = "loop"  // start again from the in point (default)
	EndHold  = "hold"  // keep showing the last frame
	EndBlack = "black" // show black
)

// Feed is one source.
type Feed struct {
	ID   string `json:"id"`   // letters, digits, - and _; referenced by layouts
	Name string `json:"name"` // shown in the UI
	Type string `json:"type"` // test, file, image
	// test
	Pattern string `json:"pattern,omitempty"` // a GStreamer videotestsrc pattern; default "smpte"
	// file, image: relative to the project file
	Path string `json:"path,omitempty"`
	// file
	InS  float64 `json:"in_s,omitempty"`  // start here (seconds)
	OutS float64 `json:"out_s,omitempty"` // stop here; 0: the end of the file
	End  string  `json:"end,omitempty"`   // loop (default), hold, black
}

// Layout places feeds on the output.
type Layout struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Boxes []Box  `json:"boxes"` // drawn in order: later boxes on top
}

// Box is a feed's place in a layout, in fractions of the output picture.
type Box struct {
	Feed  string   `json:"feed"`
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	W     float64  `json:"w"`
	H     float64  `json:"h"`
	Alpha *float64 `json:"alpha,omitempty"` // 0 to 1; default 1
}

// Recording writes the program (the mixed output) to a file.
type Recording struct {
	// Path is relative to the project file; {project}, {date} (2006-01-02) and {time}
	// (15-04-05) are replaced when the recording starts. The extension follows the container
	// when the path has none.
	Path      string `json:"path"`
	Container string `json:"container,omitempty"` // mp4 (default; fragmented, crash-safe), mkv, mov
	Codec     string `json:"codec,omitempty"`     // h264 (default), h265
	// BitrateKbps is the video bitrate; default by resolution (data in the engine).
	BitrateKbps int `json:"bitrate_kbps,omitempty"`
}

// Problems is a list of validation problems, each with its path.
type Problems []string

func (p Problems) Error() string {
	return fmt.Sprintf("%d problem(s):\n  %s", len(p), strings.Join(p, "\n  "))
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// Patterns are the test patterns offered (GStreamer videotestsrc nicks).
var Patterns = []string{"smpte", "snow", "black", "white", "red", "green", "blue", "checkers-8",
	"circular", "blink", "smpte75", "zone-plate", "gamut", "ball", "smpte100", "bar", "pinwheel",
	"spokes", "gradient", "colors"}

// Validate checks the project; dir is the project's folder (to check that files exist; "" skips
// that).
func (p *Project) Validate(dir string) error {
	var ps Problems
	add := func(path, f string, a ...any) { ps = append(ps, path+": "+fmt.Sprintf(f, a...)) }
	if p.FormatVersion != FormatVersion {
		add("format_version", "must be %d", FormatVersion)
	}
	o := p.Output
	if o.Width < 16 || o.Width > 7680 || o.Width%2 != 0 {
		add("output.width", "an even number of pixels, 16 to 7680")
	}
	if o.Height < 16 || o.Height > 4320 || o.Height%2 != 0 {
		add("output.height", "an even number of pixels, 16 to 4320")
	}
	if o.FPS < 1 || o.FPS > 120 {
		add("output.fps", "1 to 120 frames per second")
	}
	feeds := map[string]bool{}
	for i, f := range p.Feeds {
		at := fmt.Sprintf("feeds[%d]", i)
		if !idRe.MatchString(f.ID) {
			add(at+".id", "letters, digits, - and _, up to 40 characters, starting with a letter or digit")
		} else if feeds[f.ID] {
			add(at+".id", "%q is used by another feed", f.ID)
		}
		feeds[f.ID] = true
		switch f.Type {
		case FeedTest:
			if f.Pattern != "" && !contains(Patterns, f.Pattern) {
				add(at+".pattern", "one of %s", strings.Join(Patterns, ", "))
			}
		case FeedFile, FeedImage:
			if f.Path == "" {
				add(at+".path", "give the file")
			} else if dir != "" {
				if _, err := os.Stat(resolve(dir, f.Path)); err != nil {
					add(at+".path", "%v", err)
				}
			}
			if f.Type == FeedFile {
				if f.InS < 0 || f.OutS < 0 || (f.OutS > 0 && f.OutS <= f.InS) {
					add(at, "in_s and out_s must not be negative, and out_s (0: the end) must come after in_s")
				}
				if f.End != "" && f.End != EndLoop && f.End != EndHold && f.End != EndBlack {
					add(at+".end", "loop, hold or black")
				}
			}
		default:
			add(at+".type", "test, file or image")
		}
	}
	if len(p.Feeds) == 0 {
		add("feeds", "add at least one feed")
	}
	layouts := map[string]bool{}
	for i, l := range p.Layouts {
		at := fmt.Sprintf("layouts[%d]", i)
		if !idRe.MatchString(l.ID) {
			add(at+".id", "letters, digits, - and _, up to 40 characters")
		} else if layouts[l.ID] {
			add(at+".id", "%q is used by another layout", l.ID)
		}
		layouts[l.ID] = true
		seen := map[string]bool{}
		for j, b := range l.Boxes {
			bt := fmt.Sprintf("%s.boxes[%d]", at, j)
			if !feeds[b.Feed] {
				add(bt+".feed", "no feed %q", b.Feed)
			} else if seen[b.Feed] {
				add(bt+".feed", "feed %q is already in this layout (a feed appears once per layout)", b.Feed)
			}
			seen[b.Feed] = true
			if b.W <= 0 || b.H <= 0 || b.W > 1 || b.H > 1 {
				add(bt, "w and h must be above 0 and at most 1 (fractions of the output)")
			}
			if b.X < -1 || b.X > 1 || b.Y < -1 || b.Y > 1 {
				add(bt, "x and y must be within -1 and 1 (fractions of the output)")
			}
			if b.Alpha != nil && (*b.Alpha < 0 || *b.Alpha > 1) {
				add(bt+".alpha", "0 to 1")
			}
		}
	}
	if len(p.Layouts) == 0 {
		add("layouts", "add at least one layout")
	}
	if p.StartLayout != "" && !layouts[p.StartLayout] {
		add("start_layout", "no layout %q", p.StartLayout)
	}
	for i, r := range p.Recordings {
		at := fmt.Sprintf("recordings[%d]", i)
		if strings.TrimSpace(r.Path) == "" {
			add(at+".path", "give the file")
		}
		if r.Container != "" && r.Container != "mp4" && r.Container != "mkv" && r.Container != "mov" {
			add(at+".container", "mp4, mkv or mov")
		}
		if r.Codec != "" && r.Codec != "h264" && r.Codec != "h265" {
			add(at+".codec", "h264 or h265")
		}
		if r.BitrateKbps < 0 || r.BitrateKbps > 200000 {
			add(at+".bitrate_kbps", "0 (automatic) to 200000")
		}
	}
	if len(ps) > 0 {
		return ps
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// Resolve makes a path in the project absolute against the project's folder.
func Resolve(dir, p string) string { return resolve(dir, p) }

// Start returns the layout the show starts with.
func (p *Project) Start() *Layout {
	for i := range p.Layouts {
		if p.Layouts[i].ID == p.StartLayout {
			return &p.Layouts[i]
		}
	}
	if len(p.Layouts) > 0 {
		return &p.Layouts[0]
	}
	return nil
}

// Read decodes a project strictly and validates it (dir as for Validate).
func Read(r io.Reader, dir string) (*Project, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var p Project
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("more than one JSON value in the file")
	}
	if err := p.Validate(dir); err != nil {
		return nil, err
	}
	return &p, nil
}

// Load reads a project file (files it names are checked against its folder).
func Load(path string) (*Project, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := Read(f, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Write encodes the project, indented.
func (p *Project) Write(w io.Writer) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Save writes the project to a file.
func (p *Project) Save(path string) error {
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// FeedIDs lists the feeds' ids, sorted.
func (p *Project) FeedIDs() []string {
	var out []string
	for _, f := range p.Feeds {
		out = append(out, f.ID)
	}
	sort.Strings(out)
	return out
}
