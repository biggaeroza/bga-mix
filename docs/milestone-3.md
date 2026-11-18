# Milestone 3: the GUI

Status: done 2026-11-18, waiting for review.

```
./bgamix serve examples/test-pip.bgamix                  # in a web browser
go build -tags desktop,production,webkit2_41 -o bgamix-gui ./cmd/bgamix-gui
./bgamix-gui examples/test-pip.bgamix                    # in a desktop window
```

Both are the same UI (`web/`: plain ES modules, no build step) on the same API.
- **Browser:** listens on this computer only, with a session token in the URL. The Host and
  Origin headers are checked.
- **Desktop:** has native file dialogs and no network listener. Closing the window while a show
  runs asks first, and the show is stopped cleanly.

## A short guide

**The top bar.**
- **New, Open…, Save, Save as…** (Ctrl+S saves). "unsaved" marks changes.
- **The badge** reads "ready", or the number of problems (click it for the list: a missing file,
  a box for a feed that is gone …).
- **Start show / Stop show.**
  - A show runs the project as it is when started, saved or not.
  - Edits made while it runs are marked "changed since start" and apply at the next start.
- The show keeps running if the browser tab is closed. Opening the GUI again shows it.

**Feeds.**
- Add test patterns, video files, still images and playlists.
- Each file shows its thumbnail and what GStreamer makes of it: decodes or not, size, frame
  rate, duration, codec, sound.
- **File feeds:** in and out points, and what happens at the end (loop, hold, go black, or return
  to the previous layout).
- **Playlists:** their files in order.
- **ISO:** a feed can also be recorded clean, on its own.
- Renaming a feed's id renames it in every layout.

**Media.** A folder's video files and images as cards, each probed (✓ decodes, or why not) with a
thumbnail. "Add as video feed", "Add as image feed", or add to a playlist. Files inside the show's
folder are referenced relative to it, so the show moves with its media.

**Layouts.**
- **Templates:** full screen, picture in picture, side by side, grid 2×2, or empty.
- **The canvas** has the output's shape. Drag boxes to move them and their corners to resize.
  - Boxes snap to the edges, the centre lines and each other; hold Alt not to snap.
  - The box's shape is kept unless Shift is held or the box is unticked.
  - Arrow keys nudge (Shift: ×10); Delete removes.
- **The table** gives exact values: feed, position and size in %, opacity, order, full screen.
- **While a show runs:** the boxes show the live feeds, and "Take live" switches to the layout.
- **Also here:** the start layout, and the default transition (cut, or a mix of n seconds).

**Outputs.** The show's name and notes; the output size (presets from 360p to 4K, vertical
formats, or custom) and frame rate; the recordings:
- file name with {project} {date} {time};
- MP4, MKV or MOV, in H.264 or H.265;
- bitrate (automatic shown);
- starting with the show or by hand;
- a new file every n seconds or MB.

**Live** (the switcher):
- **Previews:** the program large and every feed small, as live previews.
- **Layout buttons:** a drawing of each layout. Click, or press 1–9, to take it with the chosen
  transition (the default, cut, or mix of n seconds). The layout on air is red, and "before"
  names the previous one.
- **Feeds:** each card shows the feed's state. Files and playlists have play, pause, from the
  start, previous and next, and a position bar that seeks.
- **Recordings:** record, pause, resume and stop, with elapsed time and size.

**Log.** The show's events, newest first (errors and warnings highlighted, and a filter for them),
and the show's problems. A red dot on the tab marks new errors.

## API added

The control API ([api.md](api.md)) gained the GUI's endpoints (`internal/server/studio.go`):
meta, project new/open/save/check, show start/stop, studio (status and events since n), files,
browse (native dialogs), and media and media/thumb. The GUI polls the studio
twice a second and the previews at 5 frames a second: Wails's in-process asset server carries
neither WebSockets nor endless MJPEG streams, so both shells use plain requests. The WebSocket and
MJPEG endpoints remain for other clients.

## Tests

- **Studio (`internal/app`):**
  - probing a clip with sound (size, frame rate, duration, H.264, Opus), a PNG and a broken file;
  - folder listings and relative paths;
  - start, stop and the event log;
  - save and open.
- **GUI endpoints:**
  - new, save, open, check (a missing file found), start refused with problems, start, layout,
    events after n, stop;
  - files, media and thumbnail, meta, tools, the UI's files;
  - the guard: a foreign Host or Origin is refused, and the token is needed.
- **The UI in WebKitGTK** (the desktop window's engine), driven by script against `bgamix serve`:
  every tab drawn in dark and light themes; a show started from the UI; layouts taken by click
  and key; a recording started (it plays: 43 s, 1280×720); a clip paused from its card; a box
  dragged on the canvas and the show saved (the file has the new position); the show stopped.
- **The desktop window** starts and runs. Its show controls are the same code; please try it on
  your desktop.
- **Milestones 1 and 2** still pass.

## Found along the way

- **go-gst's pbutils package** (GStreamer's discoverer) calls GStreamer type functions while the
  program loads, before GStreamer is initialised, and printed GLib criticals in every command. The
  media probe uses a plain pipeline instead. Codec names come from the tags on the pads: a paused
  pipeline posts no tag messages.
- **Thumbnails:** a keyframe seek fell back to the first frame of clips with few keyframes; the
  thumbnail seek is frame-accurate now.

## Not in milestone 3

- Audio, cameras, network sources and streaming outputs (milestone 4).
- Overlays and release packaging (milestone 5).
