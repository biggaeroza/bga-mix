# Milestone 5: overlays, packaging and documentation

Status: done 2026-11-19, waiting for review.

## Overlays

- **Types:** lower third (a name and a title on a bar with an accent strip), text (on a box or
  with a shadow), clock (time, time without seconds, date and time, date) and image (PNG,
  JPEG, GIF, BMP, TIFF, WebP; transparency kept).
- **Placement and look:**
  - position in fractions of the picture;
  - text size in fractions of its height, and width for images and lower thirds;
  - colours with their opacity, overall opacity, and fade time.
- **Showing:** they draw over every feed, in order. Each shows while it is switched on and a
  chosen layout (or any) is on air, and fades in and out. A lower third can be shown for so many
  seconds. Text and lower thirds take new words live.
- **How:** overlays are drawn by BGAMix itself (`internal/overlay`, in Go with the Go fonts), so
  they look the same on every computer with no fonts to install. Each goes over the program with
  a `gdkpixbufoverlay` element, which reloads its picture when it changes (a new name, the clock's
  next second) and fades by its alpha.
- **GUI:**
  - an Overlays tab: each overlay previewed as BGAMix draws it, dragged into place on a stage
    the output's shape;
  - an Overlays panel in the switcher: Show / On air, 8 s, and new words with Update.
- **API:** `POST /api/overlays/{id}` (on, text, subtitle, for_s); the status lists the overlays.
  `POST /api/overlay/render` draws one for previews.

## Packaging

- **`go run ./cmd/release -version x.y.z`:**
  - runs the tests and builds `bgamix` and `bgamix-gui` stamped with the version, commit and date;
  - writes `dist/<version>/`: `bgamix-<version>-linux-amd64.tar.gz` (the programs, docs,
    examples, licences, an installer, a desktop entry and an icon), the source as a tarball, the
    programs unpacked in `bin/linux-amd64/`, and `SHA256SUMS`.
- **`install.sh`** in the archive installs for the user: `~/.local/bin`, the applications menu
  and the icon.
- BGAMix links GStreamer through cgo, so each platform is built on that platform.

## Documentation

- **README:** rewritten for people meeting BGAMix for the first time, with screenshots.
- **[install.md](install.md):** GStreamer on Debian/Ubuntu, Fedora and Arch; hardware encoding;
  installing a release or building from source; troubleshooting.
- **[guide.md](guide.md):** the user guide. It covers feeds, media, layouts, overlays, outputs,
  the switcher, sound, recording, streaming (with the YouTube, Twitch and Facebook URLs),
  running without a GUI, keys, and the show file.
- **[api.md](api.md):** the overlay command.
- **`examples/studio.bgamix`:** two stand-in cameras, three layouts, a title on one layout, a
  clock and a lower third. It runs anywhere.

## Also

The program preview is now 640 pixels wide (feeds stay at 320), so it is sharp at the
switcher's size.

## Tests

| Test | Checks |
|---|---|
| Overlays in the recording | A text box shown throughout; an image shown only once its layout is taken; a hidden lower third shown live for 1 s with new words, then gone. All checked by colour in the decoded recording |
| Overlay drawing | Text size and box, the shadow-only look, lower third width, accent and bar, one line shorter than two, clock formats, an image scaled with its shape and opacity, a missing image |
| Overlay API | Rendering a preview with its position; new words with for_s; words refused on a clock; unknown overlays |
| Validation | Types, colours, positions, sizes, layouts named, image files |
| Milestones 1-4 | Still pass |

The Overlays tab and the switcher panel were checked in WebKitGTK, with a lower third shown live
over a running show. The release was built and its archive unpacked and checked.
