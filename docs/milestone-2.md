# Milestone 2: live control

Status: done 2026-11-18, waiting for review.

```
./bgamix run examples/test-pip.bgamix
curl -X POST localhost:8740/api/layout -d '{"layout": "side"}'
```

## What was built

**A new engine structure.** Every part of a show is its own GStreamer pipeline, joined by
intervideo channels: feeds → the mixer → a program channel → recordings and previews. Feeds also
go straight to their ISO recordings and previews. So:
- a feed restarts without touching the mix;
- each recording starts, pauses and stops on its own.

**Layouts, live.** `SetLayout` changes the compositor's pad properties at run time; nothing
restarts.
- **Cut:** immediate.
- **Mix:**
  - every box moves, resizes and fades from where it is to where the new layout puts it, over
    the duration (eased in and out);
  - a box that appears fades in where it lands;
  - a box that goes fades out where it was, in its place in the stack;
  - a new switch during a mix starts from the picture as it is.
- The show file may set a default transition (`"transition": {"kind": "mix", "duration_s": 0.6}`).

**Files under control.**
- Play, pause, seek (a paused file shows the frame it was seeked to), restart.
- **New end behaviour, "return":** the feed holds its last frame and the show switches back to
  the layout before (a clip that runs once and returns to the cameras).

**Playlists** (`"type": "playlist", "items": [...]`):
- the files play in turn;
- next and previous;
- at the end: loop, hold, black or return.

**Recordings.**
- Start, stop, pause and resume live; paused time is left out of the file.
- `"manual": true` waits for a start.
- **Segments:** `segment_s` and/or `segment_mb` give `name-001.mp4`, `name-002.mp4` … at key
  frames.
- **ISO recordings:** a feed's `"iso": {…}` records it clean, before mixing; `{feed}` in the path.
- A recording never overwrites an earlier file (`-2`, `-3` … are added).

**Feed isolation and reconnection.**
- A feed that fails is made transparent (the layout's other boxes show).
- It is retried after 1 s, 2 s, 4 s … up to 30 s, and the back-off resets after 10 s of good
  running.
- Events report the error, the retry and "reconnected".

**Previews:** 320-pixel JPEGs, 5 a second, of the program and every feed.

**The control API** ([api.md](api.md)):
- HTTP/JSON commands, a WebSocket for status and events, and single JPEG or MJPEG previews;
- `bgamix run` serves it on localhost;
- beyond this computer it needs a token.

## Tests

The tests run shows, act on them while they run, and decode the recordings:

| Test | Checks |
|---|---|
| Live layout | A cut and a 1 s mix land on the right colours at the right times; half-way through the mix the outgoing inset is half faded; status while mixing; an unknown layout is refused |
| Transport | Pause (status "paused"), seek while paused (the picture changes), play to the end (holds), restart |
| Playlist and return | Restarted and cut to: red item, then blue, then back to the camera layout |
| Recording controls | 2 s segments decode; a manual recording started, paused 1 s and stopped is 2 s long; the ISO recording holds the clean feed, not the program |
| Reconnect | A feed whose file is broken at the start is transparent, then reconnects once the file is good, while the mix runs on |
| Previews | The program's and each feed's JPEGs decode at 320×180 |
| API | Commands and their errors (400, 404, 405), token (query and bearer), JPEG and MJPEG, WebSocket status and events |
| Milestone 1 | Still pass |

## Found along the way

- **Preroll flash:** a video sink shows the frame decoded while a file is paused before its seek
  to the in point. That frame (before the in point) flashed into the mix. It is now hidden until
  the first seek is done.
- **Outgoing boxes in a mix:** taking the new layout's stacking order straight away, a leaving
  inset dropped behind the full-screen picture and vanished instead of fading. It now keeps its
  place while it fades.

## Not in milestone 2

- The GUI (milestone 3).
- Audio, cameras and network sources, streaming (milestone 4).
- Overlays (milestone 5).
