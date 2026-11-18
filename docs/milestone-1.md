# Milestone 1: the engine core

Status: done 2026-11-18, waiting for review.

```
./bgamix validate examples/test-pip.bgamix
./bgamix run examples/test-pip.bgamix -t 10
./bgamix tools
```

## What was built

**The show file** (`internal/project`, `.bgamix`, `format_version` 1, strict JSON, every problem
reported with its path):

| Part | Fields |
|---|---|
| `output` | `width`, `height` (even), `fps` |
| `feeds[]` | `id`, `name`, `type`: `test` (`pattern`), `file` (`path`, `in_s`, `out_s`, `end`: loop, hold or black), `image` (`path`) |
| `layouts[]` | `id`, `name`, `boxes[]`: `feed`, `x`, `y`, `w`, `h` (fractions of the output), `alpha`; later boxes on top |
| `start_layout` | the layout the show starts with (default the first) |
| `recordings[]` | `path` (`{project}`, `{date}`, `{time}`; relative to the show file), `container` (mp4, mkv, mov), `codec` (h264, h265), `bitrate_kbps` (default about 0.15 bit per pixel per frame) |

**The engine** (`internal/engine`):
- Each feed runs in its own GStreamer pipeline. It is normalised to the output (scaled to fit,
  with borders; frame rate; I420; BT.709) and handed to the mixer through an intervideo channel.
  A feed that fails leaves its slot black or held; the mix goes on (reconnection comes in
  milestone 2).
- The mixer pipeline has one compositor input per feed, placed by the layout (feeds the layout
  does not show are transparent, ready for live switching in milestone 2). The program is teed
  to every recording.
- **File feeds:**
  - they loop by seeking back to their in point at end-of-stream;
  - in and out points are an accurate seek with a stop position;
  - "hold" keeps the last frame, and "black" turns black after a second.
- **Images:** shown live (`imagefreeze`).
- **Stopping:** ends the stream after the compositor, so the recordings close cleanly with a
  complete last frame.

**Command line:** `bgamix version`, `bgamix validate`, `bgamix run [-t s]` (events printed;
Ctrl-C stops cleanly).

## Tests (they decode BGAMix's own recordings and check the pixels)

| Test | Checks |
|---|---|
| Layout in the recording | Four feeds (two test patterns, a 1 s video file with sound, a PNG) in a quad layout: each box's colour where the layout puts it; 25 fps (about 77 frames in 3 s); the clip loops twice. Passed 8 runs out of 8 |
| End behaviour | "hold" ends on the clip's last frame, "black" on black |
| In and out points | A clip of 1 s red then 1 s blue, played from 1.2 to 1.8 s: 50 blue frames, no red |
| Containers | MKV, MOV and H.265 record and decode |
| Crash safety | The show process is killed outright (SIGKILL) after 3.5 s: 77 frames play from the MP4 |
| Project, pipelines | validation paths, unknown fields, missing files; placements in pixels; pipeline descriptions; recording names; bitrates |

## Found along the way (now rules in the engine)

- **The compositor's start time:** it takes its start time from its first input. Otherwise MP4
  recordings fail ("buffer has no PTS").
- **Pinned colour format:** feeds and the mixer pin format, size, frame rate *and colorimetry*.
  Without the colorimetry:
  - sources with different colour metadata (a test pattern, a decoded file, an image) made a
    mixer input discard a feed's frames at random (the box showed black);
  - they also changed the program's format mid-stream, which made the MP4 muxer rewrite its
    header over the data after it (unplayable recordings, intermittently).
- **Stopping at the tee:** end-of-stream sent to the whole pipeline reached the feeds one by
  one, and the last recorded frame was missing some of them. It is now sent after the compositor.
- **Unbuffered files:** recordings write each buffer at once. filesink's default 64 kB of
  in-process buffering left a killed low-bitrate recording empty.
- **go-gst versions:** its v1.x releases are retracted (LGPL); BGAMix uses the current generated
  bindings (v0.0.3), which are MIT-licensed like BGAMix.

## Not in milestone 1

- Audio (milestone 4; a file's sound is decoded but not yet mixed).
- Live layout switching, transitions, file transport, playlists, reconnection, the control API
  and previews (milestone 2).
- The GUI (milestone 3).
- Cameras and network sources, streaming outputs, hardware encoders (milestone 4).
