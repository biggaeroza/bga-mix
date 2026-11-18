# Milestone 4: sources, sound and streaming

Status: done 2026-11-19, waiting for review.

## What was built

**New feed types:**
- **camera:** Video4Linux, `/dev/video0` by default. A size and frame rate can be asked for, in
  raw video or MJPEG, whichever the camera gives. The GUI lists the cameras connected.
- **stream:** a network source by URL:
  - `srt://` (`?mode=listener` waits for the sender), `rtsp://`, `udp://` (MPEG-TS), `rtmp://`,
    and `http(s)://` (HLS);
  - a buffer (SRT and RTSP; default 200 ms; RTSP's own default is 2 s);
  - a stream that drops or ends is reconnected with back-off, and its box stays transparent
    meanwhile.
- **audio:** sound only, from a microphone or line input (PulseAudio/PipeWire; the GUI lists
  them, the speakers' monitors included) or a 440 Hz test tone. It has no picture and is not
  placed in layouts.

**Sound.**
- Every feed with sound (files, playlists, streams, sound inputs) goes to a sound mixer:
  - a meter before its fader, so it shows what comes in;
  - a fader (-60 to +12 dB) and mute;
  - into the program's sound, with its own meter.
- **Modes:**
  - **follow** (the default): heard while the feed is in the layout on air. A cut switches the
    sound at once; a mix crossfades it with the picture.
  - **on:** always heard (sound inputs default to this).
  - **off.**
- Everything is changeable live (GUI, API). Recordings and streams carry the program's sound in
  AAC; ISO recordings carry the feed's own.

**Streams.**
- RTMP and RTMPS (YouTube, Twitch, Facebook …: FLV, H.264, AAC, a key frame every 2 s), with a
  stream key that is kept out of the status and logs.
- SRT (MPEG-TS; H.264 or H.265; caller or listener).
- They start with the show or by hand. A stream that drops is reconnected (1 s doubling to 30 s);
  SRT reconnects by itself and reports it.

**Encoders.**
- **auto** (the default) picks the first hardware encoder that works, from NVENC, VA-API, Quick
  Sync and the older VA-API plugin. Each is test-encoded once before it is used, so an installed
  driver that does not work falls back to x264/x265.
- Each recording and stream may name its encoder instead. The GUI shows which work on this
  computer.

**On this computer** (the switcher, the API): the program in a window of its own (for a second
screen or a projector), and its sound on the speakers.

**GUI:**
- **Feeds:** the new types and their settings, and the sound settings.
- **Outputs:** streams, and each recording's encoder and sound.
- **Switcher:**
  - a meter, mute, fader (double-click: 0 dB) and mode on each feed with sound, and a program
    meter; the meters update 8 times a second;
  - a green line under each feed that is heard;
  - Go live and Stop for streams, with their state and errors;
  - the program window and Listen.

## Tests

| Test | Checks |
|---|---|
| Audio follows video | An off-air clip is silent in the program while its own meter reads its tone; cut to it and it is heard; mute; -20 dB lowers the program by 20 dB; "on" is heard off air; the status |
| Sound in sync | A clip that turns white and beeps at the same moment: in the recording, the sound comes 24.5 ms after the picture (under a frame) |
| SRT loop | One show streams over SRT (listener); a second show takes it in as a stream feed and records it: its picture and its sound arrive |
| UDP feed | An MPEG-TS stream over UDP, placed as an inset over a test pattern |
| Stream reconnects | A stream to an SRT receiver that is not there reports it and retries; Stop ends the retries |
| Sound through loops and seeks | A looping clip's sound never stops for more than 0.4 s through 3 loops, a seek and a restart |
| Pipelines | Camera, stream, sound input and file descriptions; the sound mixer; RTMP (with its key) and SRT streams; H.265 on VA-API; meter parsing |
| Encoders | x264 and x265 work; a hardware encoder that is not here falls back to software, saying so |
| API | The sound command and its errors, meters, monitor, devices, encoders |
| Milestones 1-3 | Still pass |

The GUI was checked in WebKitGTK: the meters, faders and modes, a sound-only feed, a stream gone
live, and the new forms.

## Found along the way

- **Sound stalled after a loop or seek.** A flushing seek on a playing file left the audio sink
  (which does not wait for prerolling, so that files without sound play) on the pipeline's old
  clock base: seconds of silence after every loop. Fixes:
  - looping now uses segment seeks: no flush, and seamless;
  - a seek or restart goes through paused.
- **Stopping a stream could hang the show.** While srtsink retries a receiver it never takes
  end-of-stream, and sending one blocked. Streams are now stopped straight away (they need no
  finishing), and a recording's end-of-stream is sent so that its 10 s limit always holds.
- **The level meters' readings** come as arrays the Go bindings do not convert; they are read from
  the message text.
- **Files without sound** made a harmless "delayed linking failed" warning; it is no longer
  reported.

## Not tested on this computer

- **Cameras:** none is connected here; the pipeline is the standard one and its description is
  tested.
- **RTMP to a real service** (YouTube, Twitch): there is no RTMP server here. The FLV/RTMP
  pipeline is tested as a description, and the same encoding streams over SRT end to end.
- **RTSP:** the description is tested. It goes through the same decoder as the SRT and UDP
  streams, which are tested end to end.
- **Hardware encoders:** this computer's Intel UHD 630 needs its VA driver
  (`intel-media-va-driver-non-free`) to encode. Without it, auto chooses software, as tested.

## Not in milestone 4

Overlays (text, logos, clock, lower thirds) and release packaging are milestone 5.
