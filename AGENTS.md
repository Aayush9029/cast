# cast

A Go CLI and TUI that casts local files, `yt-dlp` URLs, and one mirrored window to a DLNA TV. It was made for a 2016 Samsung TV without AirPlay or Chromecast. It ships through `aayush9029/tap`. Status: active.

## Commands

| Task | Command |
|------|---------|
| Build | `go build -o cast .` |
| Test | `go test ./...` |
| Build the window capture helper | `cd capture && swift build -c release` |

## Architecture

- `internal/discovery` finds renderers with SSDP. `internal/target` saves the TV in `~/.config/cast/config.json` and rescans when the saved IP stops responding.
- `internal/dlna` sends AVTransport and RenderingControl SOAP calls and builds the DIDL metadata.
- `internal/cast` serves the file over HTTP (`CAST_PORT`, default `8088`) and downloads URLs with `yt-dlp` into `CAST_CACHE`.
- `internal/mirror` streams the output of `capture/` (`cast-capture`, a ScreenCaptureKit helper) to the TV for `cast window`.
- `internal/castui` and `internal/tui` hold the playback TUI.

## Rules

- The tap formula builds `cast` and `cast-capture` from source. When you change `capture/`, make sure that `swift build --package-path capture --disable-sandbox` works with no network, because Homebrew builds in a sandbox. Do not add SwiftPM dependencies to `capture/`.
- `cast` finds the helper next to its own binary or through `CAST_CAPTURE`.
- The Samsung renderer accepts H.264 and AAC in MP4. Reject other containers before playback, and print the `ffmpeg` remux command.
- `yt-dlp` reads cookies from the browser in `CAST_COOKIES` (default `safari`).
- Test a change on a real TV. Unit tests do not cover renderer behavior.
