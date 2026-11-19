// Package app is BGAMix's studio, behind the GUI: projects (new, open, save, validate), the
// running show (start, stop, its event log) and the media library (probed files with a
// thumbnail). Both shells use it through internal/server: the browser (bgamix serve) and the
// desktop window (bgamix-gui).
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
)

// eventLog is how many events the studio keeps for the UI.
const eventLog = 1000

// LoggedEvent is an event with its sequence number (the UI asks for those after the last it has).
type LoggedEvent struct {
	Seq int `json:"seq"`
	engine.Event
}

// Studio is the GUI's state; its methods may be called from any goroutine.
type Studio struct {
	mu       sync.Mutex
	show     *engine.Show
	showPath string
	log      []LoggedEvent
	seq      int

	media *mediaCache
}

// New makes a studio with no show running.
func New() *Studio { return &Studio{media: newMediaCache()} }

// NewProject is the project the UI starts from: 1280×720 at 30 fps, two test patterns, a
// picture-in-picture and a full-screen layout, and an MP4 recording next to the show file.
func NewProject() *project.Project {
	return &project.Project{
		FormatVersion: project.FormatVersion,
		Name:          "New show",
		Output:        project.Output{Width: 1280, Height: 720, FPS: 30},
		Feeds: []project.Feed{
			{ID: "bars", Name: "Colour bars", Type: project.FeedTest, Pattern: "smpte"},
			{ID: "ball", Name: "Ball", Type: project.FeedTest, Pattern: "ball"},
		},
		Layouts: []project.Layout{
			{ID: "full", Name: "Bars full screen", Boxes: []project.Box{{Feed: "bars", W: 1, H: 1}}},
			{ID: "pip", Name: "Picture in picture", Boxes: []project.Box{{Feed: "bars", W: 1, H: 1}, {Feed: "ball", X: 0.66, Y: 0.04, W: 0.3, H: 0.3}}},
		},
		Transition: &project.Transition{Kind: project.TransitionMix, DurationS: project.DefaultMixS},
		Recordings: []project.Recording{{Path: "recordings/{project}-{date}-{time}", Manual: true}},
	}
}

// Open reads a show file.
func (s *Studio) Open(path string) (*project.Project, error) {
	return project.Load(path)
}

// Save checks a project's structure (not its files, which may come later) and writes it.
func (s *Studio) Save(path string, p *project.Project) error {
	if path == "" {
		return errors.New("no file name")
	}
	if filepath.Ext(path) == "" {
		path += ".bgamix"
	}
	if err := p.Validate(""); err != nil {
		return err
	}
	return p.Save(path)
}

// Problems lists what is wrong with a project, missing files included (relative paths are
// against the show file's folder; for an unsaved show, the working folder).
func Problems(p *project.Project, path string) []string {
	dir := "." // unsaved: relative paths are against the working folder, as the engine reads them
	if path != "" {
		dir = filepath.Dir(path)
	}
	err := p.Validate(dir)
	var pr project.Problems
	if errors.As(err, &pr) {
		return pr
	}
	if err != nil {
		return []string{err.Error()}
	}
	return nil
}

// Start starts a show (stopping one that runs). path is the show file, for relative paths; an
// unsaved project uses absolute paths only.
func (s *Studio) Start(p *project.Project, path string) error {
	if pr := Problems(p, path); len(pr) > 0 {
		return project.Problems(pr)
	}
	s.Stop()
	dir := ""
	if path != "" {
		dir = filepath.Dir(path)
	}
	show, err := engine.Start(p, dir, engine.Options{Previews: true})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.show, s.showPath = show, path
	s.mu.Unlock()
	// the show's own subscription, made as it was created, has every event from the first one;
	// it is the only one read (reading a second as well logged everything twice)
	go func() {
		for e := range show.Events() {
			s.addEvent(e)
		}
	}()
	return nil
}

func (s *Studio) addEvent(e engine.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.log = append(s.log, LoggedEvent{Seq: s.seq, Event: e})
	if len(s.log) > eventLog {
		s.log = s.log[len(s.log)-eventLog:]
	}
}

// Stop stops the running show (if any), finishing its recordings.
func (s *Studio) Stop() {
	s.mu.Lock()
	show := s.show
	s.show = nil
	s.mu.Unlock()
	if show != nil {
		show.Stop()
		// let the reader log the last events ("stopped")
		for t0 := time.Now(); len(show.Events()) > 0 && time.Since(t0) < time.Second; {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Show is the running show (nil when none).
func (s *Studio) Show() *engine.Show {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.show
}

// ShowPath is the running show's file ("" when unsaved or none).
func (s *Studio) ShowPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showPath
}

// Events returns the logged events after seq (all kept ones for 0) and the last sequence number.
func (s *Studio) Events(after int) ([]LoggedEvent, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.log), func(i int) bool { return s.log[i].Seq > after })
	return append([]LoggedEvent{}, s.log[i:]...), s.seq
}

// ---- files -------------------------------------------------------------------------------

// Media files the library lists (by extension; the probe has the last word).
var (
	videoExt = map[string]bool{".mp4": true, ".mkv": true, ".mov": true, ".webm": true, ".avi": true, ".ts": true, ".m2ts": true, ".mts": true, ".mpg": true, ".mpeg": true, ".m4v": true, ".flv": true, ".ogv": true, ".wmv": true, ".mxf": true}
	imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".gif": true, ".webp": true, ".tif": true, ".tiff": true}
)

// MediaKind is "video", "image" or "" by a file's extension.
func MediaKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case videoExt[ext]:
		return "video"
	case imageExt[ext]:
		return "image"
	}
	return ""
}

// Entry is a file or folder in a listing.
type Entry struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Dir   bool      `json:"dir,omitempty"`
	Kind  string    `json:"kind,omitempty"` // video, image, show (a .bgamix file)
	Size  int64     `json:"size,omitempty"`
	MTime time.Time `json:"mtime"`
}

// Listing is a folder's contents, for the in-page file picker and the media library.
type Listing struct {
	Dir     string  `json:"dir"`
	Parent  string  `json:"parent,omitempty"`
	Entries []Entry `json:"entries"`
}

// List lists a folder (default the home folder): sub-folders, media and show files; hidden
// files are left out.
func List(dir string) (Listing, error) {
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Listing{}, err
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Dir: dir, Entries: []Entry{}}
	if p := filepath.Dir(dir); p != dir {
		l.Parent = p
	}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, de.Name())
		fi, err := os.Stat(path) // follows links
		if err != nil {
			continue
		}
		e := Entry{Name: de.Name(), Path: path, Dir: fi.IsDir(), MTime: fi.ModTime()}
		if !e.Dir {
			e.Kind = MediaKind(path)
			if strings.EqualFold(filepath.Ext(path), ".bgamix") {
				e.Kind = "show"
			}
			if e.Kind == "" {
				continue
			}
			e.Size = fi.Size()
		}
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return l, nil
}

// Relative gives path relative to the show file's folder when it lies inside it (so the show
// can move with its media), otherwise unchanged.
func Relative(showPath, path string) string {
	if showPath == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(filepath.Dir(showPath), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

func fmtErr(path string, err error) error { return fmt.Errorf("%s: %v", filepath.Base(path), err) }
