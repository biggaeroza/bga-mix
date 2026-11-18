package server

// The GUI's endpoints (with a studio):
//
//	GET  /api/meta                      version, native dialogs, the show to open first, test patterns
//	POST /api/project/new               a new project
//	POST /api/project/open   {path}     a show file ("" with native dialogs: ask)
//	POST /api/project/save   {path, project, ask}   write it ("" or ask with native dialogs: ask where)
//	POST /api/project/check  {path, project}        its problems (missing files too when it has a path)
//	POST /api/show/start     {path, project}        start it (stopping one that runs)
//	POST /api/show/stop
//	GET  /api/studio?after=N            running, the show's status, the events after N
//	GET  /api/files?dir=                a folder's sub-folders, media and show files
//	POST /api/browse         {kind, suggested}      a native dialog (desktop only)
//	GET  /api/devices                   cameras and sound inputs
//	GET  /api/media?path=               a media file's format and decode check
//	GET  /api/media/thumb?path=         its thumbnail (JPEG)

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/biggaeroza/bga-mix/internal/app"
	"github.com/biggaeroza/bga-mix/internal/engine"
	"github.com/biggaeroza/bga-mix/internal/project"
	"github.com/biggaeroza/bga-mix/internal/version"
)

func (s *Server) studioRoutes() {
	st := s.cfg.Studio
	if s.cfg.Show == nil {
		s.cfg.Show = st.Show
	}
	m := s.mux
	m.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		home, _ := os.UserHomeDir()
		writeJSON(w, 200, map[string]any{
			"version": version.Program(), "native_dialogs": s.cfg.Dialogs != nil, "initial_path": s.cfg.InitialPath,
			"home": home, "patterns": project.Patterns, "preview_fps": engine.PreviewFPS,
			"encoders": engine.Encoders(), "stream_schemes": project.StreamSchemes,
		})
	})
	m.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		cams, audio := app.Devices()
		writeJSON(w, 200, map[string]any{"cameras": cams, "audio_inputs": audio})
	})
	m.HandleFunc("POST /api/project/new", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"project": app.NewProject()})
	})
	m.HandleFunc("POST /api/project/open", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if err := readBody(r, &req); err != nil {
			writeErr(w, 400, err)
			return
		}
		if req.Path == "" {
			if s.cfg.Dialogs == nil {
				writeErr(w, 400, errors.New("give the show file"))
				return
			}
			p, err := s.cfg.Dialogs.OpenFile("show")
			if err != nil || p == "" {
				cancelled(w, err)
				return
			}
			req.Path = p
		}
		p, err := st.Open(req.Path)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, 200, map[string]any{"path": req.Path, "project": p, "problems": app.Problems(p, req.Path)})
	})
	m.HandleFunc("POST /api/project/save", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path    string           `json:"path"`
			Ask     bool             `json:"ask"`
			Project *project.Project `json:"project"`
		}
		if err := readProject(r, &req); err != nil {
			writeErr(w, 400, err)
			return
		}
		if req.Project == nil {
			writeErr(w, 400, errors.New("no project"))
			return
		}
		if req.Path == "" || req.Ask {
			if s.cfg.Dialogs == nil {
				if req.Path == "" {
					writeErr(w, 400, errors.New("give the file name"))
					return
				}
			} else {
				p, err := s.cfg.Dialogs.SaveFile("show", suggest(req.Path, req.Project.Name))
				if err != nil || p == "" {
					cancelled(w, err)
					return
				}
				req.Path = p
			}
		}
		if !strings.HasSuffix(strings.ToLower(req.Path), ".bgamix") {
			req.Path += ".bgamix"
		}
		if err := st.Save(req.Path, req.Project); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, 200, map[string]any{"path": req.Path, "problems": app.Problems(req.Project, req.Path)})
	})
	m.HandleFunc("POST /api/project/check", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path    string           `json:"path"`
			Project *project.Project `json:"project"`
		}
		if err := readProject(r, &req); err != nil || req.Project == nil {
			writeErr(w, 400, errors.Join(err, errors.New("no project")))
			return
		}
		writeJSON(w, 200, map[string]any{"problems": nonNil(app.Problems(req.Project, req.Path))})
	})
	m.HandleFunc("POST /api/show/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path    string           `json:"path"`
			Project *project.Project `json:"project"`
		}
		if err := readProject(r, &req); err != nil || req.Project == nil {
			writeErr(w, 400, errors.Join(err, errors.New("no project")))
			return
		}
		if err := st.Start(req.Project, req.Path); err != nil {
			var pr project.Problems
			if errors.As(err, &pr) {
				writeJSON(w, 422, map[string]any{"error": "the show has problems", "problems": pr})
				return
			}
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, 200, st.Show().Status())
	})
	m.HandleFunc("POST /api/show/stop", func(w http.ResponseWriter, r *http.Request) {
		st.Stop()
		writeJSON(w, 200, map[string]any{"running": false})
	})
	m.HandleFunc("GET /api/studio", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		evs, seq := st.Events(after)
		out := map[string]any{"running": false, "events": evs, "seq": seq}
		if sh := st.Show(); sh != nil {
			out["running"], out["status"], out["show_path"] = true, sh.Status(), st.ShowPath()
		}
		writeJSON(w, 200, out)
	})
	m.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
		l, err := app.List(r.URL.Query().Get("dir"))
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		writeJSON(w, 200, l)
	})
	m.HandleFunc("POST /api/browse", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind      string `json:"kind"` // show, media, image, folder
			Suggested string `json:"suggested"`
		}
		if err := readBody(r, &req); err != nil {
			writeErr(w, 400, err)
			return
		}
		if s.cfg.Dialogs == nil {
			writeErr(w, 400, errors.New("no native dialogs in the browser"))
			return
		}
		var p string
		var err error
		if req.Kind == "folder" {
			p, err = s.cfg.Dialogs.OpenFolder("Choose a media folder")
		} else {
			p, err = s.cfg.Dialogs.OpenFile(req.Kind)
		}
		if err != nil || p == "" {
			cancelled(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"path": p})
	})
	m.HandleFunc("GET /api/media", func(w http.ResponseWriter, r *http.Request) {
		md, err := st.Probe(r.URL.Query().Get("path"))
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		writeJSON(w, 200, md)
	})
	m.HandleFunc("GET /api/media/thumb", func(w http.ResponseWriter, r *http.Request) {
		md, err := st.Probe(r.URL.Query().Get("path"))
		if err != nil || md.Thumb == nil {
			writeErr(w, 404, errors.Join(err, errors.New("no thumbnail")))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(md.Thumb)
	})
}

// readProject decodes a request carrying a project; unknown fields are refused, as in files.
func readProject(r *http.Request, v any) error {
	return readBodyLimit(r, v, 4<<20)
}

func cancelled(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cancelled": true})
}

func suggest(path, name string) string {
	if path != "" {
		return path
	}
	if name == "" {
		name = "show"
	}
	return name + ".bgamix"
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
