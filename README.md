# Apple Music for BUSY Bar

A single-purpose Go process that renders Apple Music now-playing data on a BUSY Bar. It uses the stock HTTP API; no custom firmware is required.

## Design

- The entire composition has a true one-pixel outer margin. The static album cover is 14×14 at the left, followed by a one-pixel gap.
- Titles view shows the song and artist using bundled fonts. Only one row scrolls at a time: song for six seconds, pause for three, artist for six, pause for three.
- Visualizer view is a seamless 15 FPS spectrum with independent random-looking peaks, a vivid perceptual gradient built from three scored album colors, and a bright album-colored cap on every bar. Each tick updates one fixed image element; the cover is not resent.
- The view is selected once at startup with `--view titles|visualizer`; titles is the default. The process does not subscribe to runtime knob input.
- When playback pauses, timers stop and the current view is redrawn in grayscale. Playback resumes in that same view. On a cold start while paused, the API's latest album seeds that frozen grayscale screen.
- HTTP mutations use independent connections because firmware 25 is more reliable without keep-alive.

## Build

Go is pinned with `mise`:

```sh
mise install
mise run check
mise run build
```

The resulting `bin/applemusic` is self-contained.

## Run

USB uses `10.0.4.20` by default:

```sh
./bin/applemusic
```

Start directly in visualizer view:

```sh
./bin/applemusic --view visualizer
```

For Wi-Fi:

```sh
./bin/applemusic --host 192.168.1.50 --token 1234
```

The process polls `https://matteing.com/api/now-playing` every ten seconds. Use `--source` to override that endpoint. `BUSYBAR_HOST` and `BUSYBAR_TOKEN` provide equivalent environment configuration.

Press Ctrl+C to stop. Use `--keep-display` to retain the final frame.
