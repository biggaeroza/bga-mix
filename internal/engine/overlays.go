package engine

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"

	"github.com/biggaeroza/bga-mix/internal/overlay"
	"github.com/biggaeroza/bga-mix/internal/project"
)

// Overlays are drawn by internal/overlay into PNG files in the run's folder and put over the
// program by a gdkpixbufoverlay element each, after the compositor (above every feed). A change
// (a new text, the clock's next second) writes the file again and sets its location, which
// reloads it; showing and hiding fades the element's alpha.

// OverlayElement is an overlay's element in the mixer.
type OverlayElement struct {
	ID       string
	Location string
	X, Y     int
	Alpha    float64
}

func overlayChain(ovs []OverlayElement) string {
	var s string
	for _, o := range ovs {
		s += fmt.Sprintf(" ! gdkpixbufoverlay name=ov-%s location=%s offset-x=%d offset-y=%d alpha=%g", o.ID, quote(o.Location), o.X, o.Y, o.Alpha)
	}
	return s
}

type ovRun struct {
	ov        project.Overlay // with the live text
	on        bool            // the operator's switch
	alpha     float64         // now
	path      string
	gen       int // fades
	timer     *time.Timer
	clockText string
}

// drawOverlay draws an overlay into its file (written whole, then renamed into place).
func (s *Show) drawOverlay(r *ovRun) (overlay.Picture, error) {
	pic, err := overlay.Draw(r.ov, s.p.Output.Width, s.p.Output.Height, s.dir, time.Now())
	if err != nil {
		pic.Img = image.NewNRGBA(image.Rect(0, 0, 1, 1)) // nothing, but the element has a picture
	}
	tmp := r.path + ".tmp"
	f, ferr := os.Create(tmp)
	if ferr != nil {
		return pic, ferr
	}
	werr := overlay.WritePNG(f, pic.Img)
	f.Close()
	if werr != nil {
		return pic, werr
	}
	if ferr := os.Rename(tmp, r.path); ferr != nil {
		return pic, ferr
	}
	if r.ov.Type == project.OverlayClock {
		r.clockText = overlay.ClockText(r.ov, time.Now())
	}
	return pic, err
}

// startOverlaysLocked draws every overlay before the mixer starts and returns their elements.
func (s *Show) startOverlaysLocked() ([]OverlayElement, error) {
	if len(s.p.Overlays) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "bgamix-overlays-")
	if err != nil {
		return nil, err
	}
	s.ovDir = dir
	var els []OverlayElement
	for _, ov := range s.p.Overlays {
		r := &ovRun{ov: ov, on: !ov.Hidden, path: filepath.Join(dir, ov.ID+".png")}
		if overlay.Visible(ov, r.on, s.layout) {
			r.alpha = 1
		}
		pic, err := s.drawOverlay(r)
		if err != nil {
			s.emit(Event{Kind: "warning", Text: fmt.Sprintf("overlay %s: %v", ov.ID, err)})
		}
		s.ovs = append(s.ovs, r)
		els = append(els, OverlayElement{ID: ov.ID, Location: r.path, X: pic.X, Y: pic.Y, Alpha: r.alpha})
	}
	go s.clockLoop()
	return els, nil
}

// clockLoop redraws the clocks shown when their text changes.
func (s *Show) clockLoop() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		for _, r := range s.ovs {
			if r.ov.Type == project.OverlayClock && r.alpha > 0 && overlay.ClockText(r.ov, time.Now()) != r.clockText {
				s.reloadLocked(r)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Show) element(r *ovRun) gst.Element {
	if s.mixer == nil {
		return nil
	}
	return s.mixer.GetByName("ov-" + r.ov.ID)
}

// reloadLocked draws an overlay again and has its element load it.
func (s *Show) reloadLocked(r *ovRun) {
	pic, err := s.drawOverlay(r)
	if err != nil {
		s.emit(Event{Kind: "warning", Text: fmt.Sprintf("overlay %s: %v", r.ov.ID, err)})
	}
	if el := s.element(r); el != nil {
		el.SetObjectProperty("offset-x", int32(pic.X))
		el.SetObjectProperty("offset-y", int32(pic.Y))
		el.SetObjectProperty("location", r.path)
	}
}

// updateOverlaysLocked fades every overlay towards whether it should show now (the operator's
// switch and the layout on air).
func (s *Show) updateOverlaysLocked() {
	for _, r := range s.ovs {
		target := 0.0
		if overlay.Visible(r.ov, r.on, s.layout) {
			target = 1
		}
		if target == r.alpha {
			continue
		}
		if target > 0 && r.ov.Type == project.OverlayClock && overlay.ClockText(r.ov, time.Now()) != r.clockText {
			s.reloadLocked(r) // a clock coming back shows the time now
		}
		r.gen++
		gen, from := r.gen, r.alpha
		frames := max(1, int(overlay.Fade(r.ov)*float64(s.p.Output.FPS)))
		period := time.Second / time.Duration(s.p.Output.FPS)
		go func() {
			tick := time.NewTicker(period)
			defer tick.Stop()
			for k := 1; k <= frames; k++ {
				<-tick.C
				s.mu.Lock()
				if r.gen != gen || s.stopped {
					s.mu.Unlock()
					return
				}
				r.alpha = from + (target-from)*float64(k)/float64(frames)
				if k == frames {
					r.alpha = target
				}
				if el := s.element(r); el != nil {
					el.SetObjectProperty("alpha", r.alpha)
				}
				s.mu.Unlock()
			}
		}()
	}
}

// OverlayCommand changes an overlay live: on shows or hides it (forS > 0: shown for that long,
// then hidden), text and subtitle replace its words (text, clock: no; lower third: both).
func (s *Show) OverlayCommand(id string, on *bool, text, subtitle *string, forS float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("the show has stopped")
	}
	var r *ovRun
	for _, x := range s.ovs {
		if x.ov.ID == id {
			r = x
		}
	}
	if r == nil {
		return fmt.Errorf("no overlay %q", id)
	}
	if forS < 0 || forS > 3600 {
		return errors.New("for_s is 0 to 3600")
	}
	if text != nil || subtitle != nil {
		if r.ov.Type != project.OverlayText && r.ov.Type != project.OverlayLowerThird {
			return errors.New("only text and lower thirds have words to change")
		}
		if text != nil {
			r.ov.Text = overlay.Strip(*text)
		}
		if subtitle != nil {
			r.ov.Subtitle = overlay.Strip(*subtitle)
		}
		s.reloadLocked(r)
	}
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if forS > 0 {
		t := true
		on = &t
		r.timer = time.AfterFunc(time.Duration(forS*float64(time.Second)), func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.stopped {
				return
			}
			r.on = false
			s.updateOverlaysLocked()
			s.emit(Event{Kind: "overlay", Text: id + " off"})
		})
	}
	if on != nil {
		r.on = *on
	}
	s.updateOverlaysLocked()
	what := map[bool]string{true: "on", false: "off"}[r.on]
	if forS > 0 {
		what += fmt.Sprintf(" for %.0f s", forS)
	}
	s.emit(Event{Kind: "overlay", Text: id + " " + what})
	return nil
}

// OverlayStatus is an overlay's state.
type OverlayStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"`
	On       bool   `json:"on"`
	Visible  bool   `json:"visible"` // shown now (on, and on this layout)
	Text     string `json:"text,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
}

func (s *Show) overlayStatusLocked() []OverlayStatus {
	out := []OverlayStatus{}
	for _, r := range s.ovs {
		out = append(out, OverlayStatus{ID: r.ov.ID, Name: r.ov.Name, Type: r.ov.Type, On: r.on,
			Visible: overlay.Visible(r.ov, r.on, s.layout), Text: r.ov.Text, Subtitle: r.ov.Subtitle})
	}
	return out
}
