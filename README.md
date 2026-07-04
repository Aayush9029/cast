<p align="center">
  <img src="assets/icon.png" width="128" alt="cast">
  <h1 align="center">cast</h1>
  <p align="center">Stream a video file to a Samsung Smart TV</p>
</p>

<p align="center">
  <a href="https://github.com/Aayush9029/cast/releases/latest"><img src="https://img.shields.io/github/v/release/Aayush9029/cast" alt="Release"></a>
  <a href="https://github.com/Aayush9029/cast/blob/main/LICENSE"><img src="https://img.shields.io/github/license/Aayush9029/cast" alt="License"></a>
</p>

## Install

```bash
brew install aayush9029/tap/cast
```

Or tap first:

```bash
brew tap aayush9029/tap
brew install cast
```

## Usage

```bash
cast ~/Movies/movie.mp4         # cast a local file
cast https://example.com/video  # any yt-dlp-supported URL
cast window                     # mirror one window + its audio to the TV
cast discover                   # pick a TV (only needed when you have several)
cast stop                       # stop playback
```

While casting, use the TUI controls: `space` toggles play/pause, `←`/`→` seek 15 seconds, `↑`/`↓` adjust volume, `s` stops playback, and `q` quits.

The TV is auto-discovered via SSDP on first run. Subsequent casts go to the saved TV at `~/.config/cast/config.json`; if that IP stops responding, `cast` rescans the local network and updates the saved target. URL casting needs `yt-dlp` and `ffmpeg` (`brew install yt-dlp ffmpeg`).

Samsung's DLNA renderer expects H.264/AAC in an MP4 container. An `.mkv` (or other non-MP4) file may be refused by the TV — `cast` rejects it up front with the exact `ffmpeg -i in.mkv -c copy out.mp4` command to remux it (near-instant when the codecs already match).

## Window mirroring

`cast window` lists your open windows, mirrors the one you pick — video **and** its audio — to the TV. It uses a small Swift/ScreenCaptureKit helper (`cast-capture`) that captures the window's audio directly, so no virtual audio device (BlackHole etc.) is needed. Grant **Screen Recording** permission the first time (System Settings ▸ Privacy & Security ▸ Screen Recording).

```bash
cast window          # pick a window, then it mirrors to the TV
CAST_FPS=24 cast window
```

The helper ships with the Homebrew formula. From source, build it once:

```bash
cd capture && swift build -c release
export CAST_CAPTURE="$(swift build -c release --show-bin-path)/cast-capture"
# or copy that binary next to `cast` / onto your PATH
```

Caveats: this streams live over DLNA, which is inherently TV-dependent and runs a few seconds behind — great for slides, video, and demos, not for twitch gaming. It captures a single window, not the whole desktop.

## Why this exists

At a friend's place. TV in the room, but no way to actually use it. 2016 Samsung, so no AirPlay (Samsung only added it from 2018 onward), no Chromecast, and the browser on the TV itself couldn't play any of the streaming sites I wanted.

First tried spinning up a little local website to host the videos on and visit from the TV browser. That didn't work. So here this is.

## License

MIT
