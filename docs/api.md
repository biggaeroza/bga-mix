# The control API

`bgamix run` serves it on `127.0.0.1:8740` (`-listen addr` to change, `-listen none` for none).
Commands are HTTP with JSON; events and status come over a WebSocket. Successful commands answer
with the show's status.

## Access

- On this computer only by default.
- To listen beyond this computer (`-listen 0.0.0.0:8740`), a token is required: `-token` or
  `$BGAMIX_TOKEN`. Every request then carries `Authorization: Bearer <token>` or `?token=<token>`.
- The token travels in plain HTTP: on a network you do not trust, put the API behind an HTTPS
  proxy or an SSH tunnel.
- Browsers may only open the WebSocket from the same origin.

## Endpoints

| Request | Does |
|---|---|
| `GET /api/status` | The show: layout, previous layout, transitioning; feeds (state, position, duration, playlist item, last error, restarts; sound: mode, level, mute, how much is heard, meter); recordings (state, file, recorded time, size, encoder); streams (state, URL without its key, time live, encoder, last error); the program's meter; the monitor |
| `POST /api/layout` `{"layout": "side"}` | Switches with the show's default transition |
| `POST /api/layout` `{"layout": "side", "transition": {"kind": "mix", "duration_s": 1}}` | Switches with this transition (`cut`, or `mix` over 0 to 60 s) |
| `POST /api/feeds/{id}/play` | Plays a paused or ended file or playlist, or restarts a stopped feed |
| `POST /api/feeds/{id}/pause` | Pauses a file or playlist (it holds its frame) |
| `POST /api/feeds/{id}/restart` | A file back to its in point; a playlist back to its first item |
| `POST /api/feeds/{id}/seek` `{"seconds": 12.5}` | Seeks a file (kept within its in and out points) or the current playlist item; a paused file shows the new frame |
| `POST /api/feeds/{id}/next`, `/previous` | A playlist's next or previous item |
| `POST /api/recordings/{label}/start`, `/stop`, `/pause`, `/resume` | A recording: `program-1`, `program-2` … in the show file's order, `iso-<feed>` for ISO recordings. A pause is left out of the file. Streams (`stream-1` …) start and stop (they do not pause) |
| `POST /api/feeds/{id}/audio` `{"volume_db": -6, "mute": true, "mode": "follow"}` | A feed's sound, any of: level (-60 to +12 dB), mute, and mode (`follow`: heard while on air; `on`; `off`) |
| `GET /api/levels` | The meters, 10 readings a second: `{"program": {"peak_db", "rms_db"}, "<feed>": …}` in dBFS (-100: silence); a feed's is measured before its fader |
| `POST /api/monitor/window`, `/listen` `{"on": true}` | The program in a window on the mixing computer; its sound on that computer's speakers |
| `GET /api/preview/{key}` | The latest JPEG (320 px wide, 5 a second) of `program` or a feed |
| `GET /api/preview/{key}/stream` | The same as an MJPEG stream (opens in a browser or VLC) |
| `GET /api/ws` | WebSocket: `{"type": "status", "status": …}` at once and every second; `{"type": "event", "event": {"time", "feed", "recording", "kind", "text", "fatal"}}` as things happen |

Errors are `{"error": "…"}` with 400 (a bad request), 404 (no such layout, feed or recording),
405 (wrong method) or 401 (no token).

## Examples

```
A=localhost:8740/api
curl $A/status
curl -X POST $A/layout -d '{"layout": "side", "transition": {"kind": "mix", "duration_s": 1.5}}'
curl -X POST $A/feeds/clip/seek -d '{"seconds": 30}'
curl -X POST $A/recordings/program-1/pause
```

Event kinds: `started` (the show; a feed reconnected), `layout`, `audio`, `window`, `listen`, `looped`, `next`, `ended`,
`paused`/`play`/`seek`/`restart` (commands), `error`, `warning`, `restarting`, `recording` (a file
started), `stopped` (a recording or the show), `start`/`pause`/`resume` (recording commands).
