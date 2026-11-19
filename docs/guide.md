# BGAMix user guide

BGAMix mixes live video. You describe a **show**: its **feeds** (the sources), its **layouts**
(where the feeds go on the picture), its **overlays** (titles, logos, a clock) and its
**outputs** (recordings and streams). Then you start it and switch between layouts live.

A show is saved as a `.bgamix` file. Media files it uses can sit next to it, in the same folder
or below; BGAMix refers to them relative to the show, so the folder can be moved or copied to
another computer as a whole.

- [The window](#the-window)
- [Feeds](#feeds)
- [Media](#media)
- [Layouts](#layouts)
- [Overlays](#overlays)
- [Outputs](#outputs)
- [Running a show: the switcher](#running-a-show-the-switcher)
- [Sound](#sound)
- [Recording](#recording)
- [Streaming](#streaming)
- [Without the GUI](#without-the-gui)
- [Keys](#keys)
- [The show file](#the-show-file)

## The window

**The top bar:**
- **New, Open…, Save, Save as…** (Ctrl+S saves); "unsaved" marks changes not yet saved.
- **The badge:** "ready", or how many problems the show has. Click it for the list: a file that
  is missing, a box for a feed that was deleted, a stream without a URL.
- **Start show / Stop show.**

**Starting and stopping:**
- A show that has problems cannot start.
- A show runs as it was when you started it. It does not need saving first.
- Edits made while it runs are marked "changed since start" and apply the next time you start
  it. Live changes (layouts, overlays, sound, recordings) are made in the switcher.
- Stopping finishes the recordings properly.
- In the browser, the show keeps running when the tab is closed; open the GUI again to carry on.
  The desktop window asks before closing with a show running.

**The tabs:** Live (the switcher), Feeds, Media, Layouts, Overlays, Outputs and Log.

## Feeds

![Feeds](images/feeds.png)

Add a feed with **+ Add…** and choose its type:

| Type | What it is | Settings |
|---|---|---|
| Camera | A USB webcam, a laptop camera, an HDMI or SDI capture card | The device (connected ones are listed); a size and frame rate to ask of it, or the camera's own |
| Network stream | Video from another machine, an IP camera or an encoder | The URL (below); a buffer for SRT and RTSP (default 200 ms: more rides out a poor network, less is closer to live) |
| Video file | A clip | The file; in and out points; at the end: loop, hold the last frame, go black, or return to the previous layout |
| Playlist | Clips played in turn | The files, in order; at the end of the last: as for a file |
| Still image | A slide, a holding card | The file |
| Test pattern | Colour bars and other patterns, for trying layouts out | The pattern |
| Sound input | A microphone, a line input, or what another program plays (the "monitor" of your speakers) | The input, or a test tone |

**Network stream URLs:**
- `srt://0.0.0.0:9000?mode=listener`: waits for a sender on port 9000. `srt://host:9000` calls a
  sender that listens.
- `rtsp://user:password@camera/stream`: an IP camera (see its manual for the path).
- `udp://0.0.0.0:5000`: MPEG-TS over UDP, as many encoders send it.
- `rtmp://server/app/key` and `https://…/playlist.m3u8` (HLS) are read too.

**When a feed fails:**
- A camera unplugged, a stream that drops or a broken file turns its box transparent.
- BGAMix tries again after 1, 2, 4 … up to 30 seconds, and the rest of the show carries on.
- The Log tab says what happened.

**The id:** each feed has an id (letters, digits, - and _) that layouts and the control API use.
Renaming it updates every layout.

**ISO recording:** tick "Record this feed on its own" to record it clean, before mixing, with
its own sound: useful for editing later.

## Media

The Media tab shows the video files and images in a folder (your show's folder to begin with).
Each is checked:
- whether GStreamer can decode it (✓, or what is wrong);
- its size, frame rate, length, codec and sound;
- a thumbnail.

From a card you can add the file as a feed, or add a clip to a playlist.

## Layouts

![Layouts](images/layouts.png)

A layout places feeds on the picture.
- **Add one** from a template (full screen, picture in picture, side by side, 2×2 grid), or start
  empty, and add a box for each feed you want in it.
- **On the canvas** (the shape of your output):
  - drag a box to move it, and drag its corners to resize it;
  - boxes snap to the edges, the centre lines and each other (hold **Alt** not to snap);
  - the box keeps its shape while resizing unless you hold **Shift** or untick "keep the box's
    shape";
  - **arrow keys** nudge the chosen box (Shift: ten times as far); **Delete** removes it.
- **The table below** gives exact values: position and size in percent of the picture, opacity,
  front to back order, and full screen.
- **"The show starts with this layout"** marks the start layout.
- **The default transition** (cut, or a mix of so many seconds) is used when you switch, unless
  the switcher says otherwise.
- A feed can appear once in each layout. A feed that a layout leaves out is not seen, but keeps
  running, ready to be cut to.
- While a show runs, the boxes show the live feeds, and **Take live** switches to the layout you
  are editing.

## Overlays

![Overlays](images/overlays.png)

Overlays are drawn over the program, above every feed:

| Type | For |
|---|---|
| Lower third | Introducing someone: a name and a title on a bar with an accent strip |
| Text | A line of text, on a box or with a shadow |
| Clock | The time, or the date and time (the mixing computer's clock) |
| Image | A logo or a corner bug; PNG transparency is kept |

**Editing:**
- Each overlay is previewed exactly as BGAMix will draw it, on a checkered stage the shape of the
  picture. Drag it to place it.
- **Settings:** text size (in percent of the picture's height), width (images, lower thirds),
  colours with their opacity (text, the bar or box, the accent strip), overall opacity and fade
  time.
- **"Starts hidden":** typical for lower thirds, which you show when the person speaks.
- **"Shown on":** every layout, or only some. A logo can stay off the full-screen title card,
  for example.

**Live, in the switcher:**
- **Show / On air** switches an overlay on or off. It fades in or out, and shows only on its
  layouts.
- **8 s** shows a lower third for eight seconds, then hides it.
- **New words:** type a new name and title and press **Update** (or Enter). The overlay
  changes while on air, so one lower third can introduce everyone in turn.

## Outputs

**The show:** its name (used in recording file names) and notes.

**The output picture:**
- **Size:** 360p to 4K, vertical formats for phones, or your own.
- **Frame rate.**
- Every feed is scaled to fit, keeping its shape (with black borders where the shapes differ),
  and brought to this frame rate.

**Recordings and streams:** see [Recording](#recording) and [Streaming](#streaming).

## Running a show: the switcher

![The switcher](images/switcher.png)

Press **Start show**. The Live tab shows:

- **The program:** what is recorded and streamed, with its sound meter, the layout on air and the
  one before it.
- **Layouts:** a button for each, drawn small. Click one, or press its number (1–9), to take it.
  - The transition used is the default, a cut, or a mix of the seconds you set (Transition panel).
  - A new switch during a mix starts from the picture as it is.
- **Feeds:** each with its live picture and state (playing, paused, ended, restarting).
  - **Clips:** play, pause, from the start, and a position bar to seek (also while paused).
  - **Playlists:** previous and next as well.
  - **Feeds with sound:** a meter, mute (M), a fader (double-click: back to 0 dB) and the sound
    mode.
- **Recordings:** record, pause, resume and stop, with the time and size so far.
- **Overlays:** see above.
- **Streams:** Go live and Stop, with their state and errors.
- **On this computer:**
  - **Program window** opens the program full size in a window, for a second screen or a
    projector.
  - **Listen** plays the program's sound on this computer's speakers.

## Sound

Every feed with sound (clips, playlists, network streams, sound inputs) goes to a sound mixer.

**Modes:**
- **AFV** (audio follows video, the default for picture feeds): a feed is heard while it is in
  the layout on air. A cut switches its sound at once; a mix crossfades it with the picture.
- **On:** always heard (the default for sound inputs). For a presenter's microphone, or music
  under everything.
- **Off:** not heard.

**Levels:**
- Each feed's fader runs from -60 to +12 dB.
- Its meter shows the sound as it comes in, before the fader, so you can see a microphone even
  while it is faded out.
- The program meter shows what goes out. Keep its peaks out of the red (above -6 dBFS).

## Recording

Outputs → **+ Add a recording**:

- **File name:** `{project}`, `{date}` and `{time}` are filled in when it starts; the extension
  follows the container. Relative names go next to the show file; a show not yet saved records in
  `BGAMix` in your videos folder (usually `~/Videos/BGAMix`). `~/` stands for your home folder. A
  recording never overwrites another: `-2`, `-3` … are added. The Log tab shows each file's full
  path when it starts.
- **Container:**
  - **MP4** and **MOV** are written so that a recording cut short by a crash or power cut still
    plays up to its last second.
  - **MKV** is the other option.
- **Codec:** H.264 plays everywhere; H.265 is about 40 % smaller and slower to encode.
- **Bitrate:** automatic (about 4 Mbit/s for 720p30, 9 Mbit/s for 1080p30), or your own.
- **Encoder:** automatic (the graphics chip if it can, otherwise software), or a named one.
- **Sound:** the program's (AAC, 160 kbit/s by default), or none.
- **Start:** with the show, or by hand from the switcher.
- **New file every:** so many seconds or megabytes (`name-001.mp4`, `name-002.mp4` …).

## Streaming

Outputs → **+ Add a stream**:

| Service | Server URL | Key |
|---|---|---|
| YouTube | `rtmp://a.rtmp.youtube.com/live2` | YouTube Studio → Go live → Stream key |
| Twitch | `rtmp://live.twitch.tv/app` | Twitch dashboard → Settings → Stream |
| Facebook | `rtmps://live-api-s.facebook.com:443/rtmp/` | Live Producer → Stream key |
| Another machine | `srt://host:port` (`?mode=listener` to let it call you) | none |

**Settings:**
- The video bitrate: automatic by the picture size, or what the service recommends.
- The sound bitrate, the codec (H.264; H.265 over SRT only) and the encoder.
- Whether it starts with the show or by hand (**Go live** in the switcher).

**While streaming:**
- A stream that drops reconnects by itself.
- Streams do not pause: to hold the picture, cut to a holding layout (a still image) instead.

**The stream key** is stored in the show file and is hidden in the switcher and the logs. Share
show files with care.

## Without the GUI

- **`bgamix run show.bgamix`** runs a saved show on a machine without a screen.
- It is controlled through the [control API](api.md) on port 8740 (this computer only, unless you
  give `-listen` and `-token`). Ctrl-C stops it cleanly.
- `bgamix validate show.bgamix` checks a show file.
- `bgamix serve` runs the GUI for a browser on the same computer. To use it from another
  computer, reach it through an SSH tunnel (`ssh -L 8740:localhost:8740 mixer`).

## Keys

| Where | Key | Does |
|---|---|---|
| Anywhere | Ctrl+S | Save |
| Live | 1–9 | Take layout 1–9 |
| Layouts | Arrows (Shift: ×10) | Nudge the chosen box |
| Layouts | Delete | Remove the chosen box |
| Layouts | Alt while dragging | Do not snap |
| Layouts | Shift while resizing | Do not keep the shape |
| Live | Enter in an overlay's text | Update the overlay |

## The show file

A `.bgamix` file is JSON, so it can also be written by hand or by a program. BGAMix checks it
when it is opened and lists every problem with where it is (`feeds[2].path: no such file`).
`examples/` has examples. In outline:

```json
{
  "format_version": 1,
  "name": "Sunday service",
  "output": { "width": 1920, "height": 1080, "fps": 30 },
  "feeds": [
    { "id": "cam1", "name": "Camera 1", "type": "camera", "device": "/dev/video0" },
    { "id": "slides", "type": "stream", "url": "srt://0.0.0.0:9000?mode=listener" },
    { "id": "intro", "type": "file", "path": "media/intro.mp4", "end": "return" },
    { "id": "mic", "type": "audio", "device": "alsa_input.usb-mic" }
  ],
  "layouts": [
    { "id": "cam", "name": "Camera", "boxes": [ { "feed": "cam1", "x": 0, "y": 0, "w": 1, "h": 1 } ] },
    { "id": "slides", "name": "Slides + camera", "boxes": [
        { "feed": "slides", "x": 0, "y": 0, "w": 1, "h": 1 },
        { "feed": "cam1", "x": 0.7, "y": 0.68, "w": 0.28, "h": 0.28 } ] }
  ],
  "transition": { "kind": "mix", "duration_s": 0.5 },
  "overlays": [
    { "id": "name", "type": "lower_third", "text": "Jane Doe", "subtitle": "Speaker", "x": 0.05, "y": 0.78, "hidden": true },
    { "id": "logo", "type": "image", "path": "media/logo.png", "x": 0.86, "y": 0.04, "w": 0.1 }
  ],
  "recordings": [ { "path": "recordings/{project}-{date}-{time}" } ],
  "streams": [ { "name": "YouTube", "url": "rtmp://a.rtmp.youtube.com/live2", "key": "…", "manual": true } ]
}
```

Positions and sizes are fractions of the picture (0 to 1), so a show keeps its look at any output
size.
