# BGAMix: plan

Status: **approved 2026-11-18**: MIT licence, public repository `biggaeroza/bga-mix`. All five
milestones are done (2026-11-19); see the milestone notes in this folder.

BGAMix is a live video mixer for people who want Snowmix's kind of tool without scripting it:
a mixer that runs on a desktop, a server or a small box, takes cameras, streams and files as
feeds, places them in layouts, switches between layouts live, and records or streams the result.
It is set up and run from a GUI (desktop window or web browser) and can be controlled remotely.

## 1. Independence from Snowmix

BGAMix is **new software, written from scratch in Go**. It is inspired by Snowmix by Peter
Maersk-Moller (snowmix.sourceforge.net) and acknowledges it, but contains nothing of it. This
respects his terms (Snowmix's LICENSE forbids selling it or distributing it with hardware, also
for forks) by not being a fork at all.

Rules for the work:

- **No Snowmix material is copied or consulted** while writing BGAMix:
  - no source, scripts, `.ini` files, documentation text, command syntax, fonts, images or clips.
  - Before this plan, only its licence files, README, file list and one example `.ini` were read,
    to judge the licence and the architecture. Nothing from them is used.
- **Ideas are not copyrighted and are fair to share:** feeds, layouts, overlays, a live control
  interface. BGAMix designs its own: its own project format, layout model, control API and UI.
- **The engine is GStreamer's,** used as a library (`compositor`, `audiomixer`, overlays, codecs,
  network sources and sinks). Snowmix's mixing engine is not reimplemented.
- **Naming:** "Snowmix" is not used in the name, the code or the marketing. The README says
  "Inspired by Snowmix by Peter Maersk-Moller", with a link.

## 2. Licence: MIT (decided 2026-11-18)

The code is ours, so the licence is our choice. The dependencies don't constrain it:
- GStreamer is LGPL-2.1, used through dynamic linking; its go-gst bindings are MIT;
- Wails is MIT;
- Go is BSD.

MIT: anyone, BIG-G Aerospace included, may use, modify, sell or bundle it,
keeping the copyright notice. The other options considered were Apache-2.0 (with a patent grant)
and GPLv3 (copyleft).

## 3. Repository: public (decided 2026-11-18)

`biggaeroza/bga-mix` (product name **BGAMix**), named like the other tools, with the same
working rules: milestones with a review stop after each, every value with its source, README,
THIRD-PARTY.md, and a release tool like the others'. Public from the first commit.

## 4. Architecture

```
            feed pipelines (one per feed, isolated)             mixer pipeline
 camera ──► v4l2src ─► … ─► intervideosink "f1" ─┐      intervideosrc "f1" ─┐
 RTSP   ──► rtspsrc ─► decode ─► intervideosink "f2" ┼──►   intervideosrc "f2" ─┼─► compositor ─┬─► preview (JPEG to the UI)
 file   ──► uridecodebin ─► intervideosink "f3" ─┘      intervideosrc "f3" ─┘   + audiomixer   ├─► record (file)
                                                                                                 └─► stream (RTMP, SRT)
            bgamix (Go): project ─► pipelines; supervisor; live control; HTTP/WebSocket API; GUI
```

- **Feed isolation:** each feed runs in its own pipeline and hands frames to the mixer through
  `intervideo`/`interaudio`. A camera that disconnects or a stream that stalls never stops the
  mix: its slot shows black (or a "no signal" card), and the supervisor restarts the feed with
  back-off.
  - This was proven on this machine (GStreamer 1.28): two isolated live feeds composited into a
    1280×720 picture-in-picture at 30 fps.
- **Live layouts:** a layout gives each feed a box (position, size, crop, opacity, order). The
  compositor's pad properties are changed at run time, and a transition (cut, or a timed move and
  fade) is computed in Go and applied frame by frame. Nothing restarts.
- **Go program** (`bgamix`):
  - project model and validation;
  - pipeline builder (go-gst);
  - process and pipeline supervisor with logs and statistics (fps, drops, bitrate);
  - control API: HTTP/JSON for commands, WebSocket for events and status;
  - preview: a low-rate JPEG stream of the program output and each feed, for the UI;
  - CLI: `bgamix run project.bgamix` (headless), `bgamix serve` (UI in a browser),
    `bgamix version`, `bgamix tools`;
  - `bgamix-gui`: the desktop window (Wails), the same UI.
- **Project file** (`.bgamix`, versioned JSON, strict, validated with paths like the other tools):
  output format (size, frame rate), feeds, layouts (scenes), overlays, outputs, audio. Our own
  format.
- **Hardware encoding:** used when present (VA-API, NVENC), falling back to x264.

## 5. Existing videos: as feeds and as outputs

Pre-recorded video is a first-class part of a show (intros, clips, replays of earlier
recordings, adverts, holding loops), and so is recording to files.

### As feeds (sources)

**Video file feed:** any file GStreamer can decode (MP4, MKV, MOV, WebM, MPEG-TS, AVI …),
with its audio. Each file feed has:
- **Transport:** play, pause, stop, restart, seek; controlled live from the switcher and the
  control API.
- **In and out points:** play only a part of the file.
- **End behaviour:** loop, hold the last frame, go black, or cut back to the previous layout
  (for clips that run once and return to the cameras).
- **Speed:** normal speed, or slowed down (a replay feel).

**Other file sources:**
- **Playlist feed:** an ordered list of files (or a folder) played in turn, looping or once,
  with skip next and previous. Useful for holding loops and advert breaks.
- **Media library:** files and folders added to the project, with thumbnails, duration, format
  and a check that each one decodes before the show. Unplayable files are flagged with the
  reason.
- **Still images:** PNG, JPEG, SVG as a feed: title cards, holding slides, "be right back".

### As outputs

**Program recording:** the mixed output to a file.
- **Container:** MP4 (fragmented, so a crash leaves a playable file), MKV or MOV.
- **Settings:** codec and quality (H.264 or H.265, bitrate or constant quality), audio codec.
- **File handling:** split into segments by time or size if wanted, and named by date and time.
- **Controls:** start, stop and pause recording live, independently of streaming.

**Other file outputs:**
- **ISO recordings:** each feed also recorded on its own, clean and unmixed, for editing
  afterwards. Off by default, per feed.
- **Instant clip (later):** mark in and out during the show and save that part as a file, which
  can then be played back as a feed.
- **Recordings back into the library:** a finished recording appears in the media library, so
  it can be played back in the same session.

### Proven on this machine (2026-11-18, GStreamer 1.28)

A video file played as a feed beside a live feed, composited at 1280×720, 30 fps and recorded
as H.264 in a fragmented MP4. The recorder was then killed outright (`kill -9`, a simulated
crash): 147 frames decode from the file. Two rules came out of it and go into the engine:

- The compositor must take its start time from its first input (`start-time-selection=first`).
  Otherwise its first frames reach the MP4 muxer without timestamps and the recording fails
  (Matroska tolerates it; MP4 does not).
- Crash-safe MP4 recording must be **fragmented and streamable** (the header written first,
  then fragments with key frames each second). A plain MP4 written by the same pipeline was
  unplayable when the process was killed, because its index is written only at the end.

## 6. Milestones (each ends with a review stop)

1. **Engine core:**
   - project format;
   - feeds: test pattern, video file (loop, in and out points, end behaviour), still image;
   - a fixed layout;
   - program recording to MP4 or MKV with codec settings;
   - `bgamix run`;
   - tests headless (frames checked for content and timing; recordings decoded back and
     checked).
2. **Live control:**
   - layouts (full screen, picture-in-picture, side by side, grid, custom);
   - cut and timed transitions;
   - file feed transport (play, pause, seek, restart) and "cut back when the clip ends";
   - playlist feeds;
   - recording start, stop and pause, segmenting, ISO recordings;
   - feed isolation and automatic reconnection;
   - the HTTP/WebSocket control API;
   - JPEG previews.
3. **GUI** (Wails window and browser), with project open and save:
   - a feed editor;
   - the media library (thumbnails, durations, a decode check);
   - clip transport in the switcher;
   - a layout editor (drag and resize boxes on a canvas);
   - outputs;
   - Start and Stop;
   - a live switcher with feed thumbnails and program preview;
   - status and logs.
4. **More sources and outputs:**
   - inputs: USB/V4L2 cameras, RTSP/IP cameras, SRT/UDP/RTMP input;
   - outputs: RTMP (YouTube, Twitch …), SRT and a local preview window;
   - audio mixing per feed with levels and meters;
   - hardware encoders.
5. **Overlays and packaging:**
   - text, image/logo and clock overlays (in layouts);
   - lower thirds;
   - release archives like the other tools';
   - docs.

## 7. Needed on this machine

The GStreamer runtime and its plugins are installed (every element the design uses is present).
Building against GStreamer needs the development packages, installed by you with sudo:

```
sudo apt install libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev libgstreamer-plugins-bad1.0-dev
```

## 8. Out of scope (for now)

- Multi-machine setups.
- WebRTC contribution feeds (possible later: `webrtcbin` is present).
- Instant replay with slow motion from a live buffer (instant clips from recordings are in scope).
- Chroma key.
- Windows and macOS builds: Linux first; GStreamer runs on both, so they are possible later.
