# BUSY Bar Apps

[![CI](https://github.com/matteing/busybar-apps/actions/workflows/ci.yml/badge.svg)](https://github.com/matteing/busybar-apps/actions/workflows/ci.yml)
[![GitHub release](https://img.shields.io/github/v/release/matteing/busybar-apps)](https://github.com/matteing/busybar-apps/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A single, self-contained Go binary for running host-side apps on a BUSY Bar. Apps use the release firmware HTTP API; they do not install custom firmware.

## Install

Download the archive for your Mac from [GitHub Releases](https://github.com/matteing/busybar-apps/releases/latest):

- `darwin_arm64` for Apple Silicon Macs
- `darwin_amd64` for Intel Macs

Extract the archive, make the binary executable, and move it somewhere on your
`PATH`:

```sh
chmod +x busyctl
install -m 755 busyctl ~/.local/bin/busyctl
busyctl version
```

Release archives include SHA-256 checksums in `checksums.txt`.

## Toolchain

The project pins Go with `mise`:

```sh
mise install
```

## Build

```sh
mise run build
```

The resulting `bin/busyctl` file has no runtime dependencies.

Install it on your path:

```sh
install -m 755 bin/busyctl ~/.local/bin/busyctl
```

## Use

Run without arguments for an interactive app selector:

```sh
./bin/busyctl
```

Or select an app directly:

```sh
./bin/busyctl apps
./bin/busyctl apple-music
./bin/busyctl hacker-news
```

`busyctl status` checks connectivity and reports the device API version. Display
content can be removed by app, without disturbing other apps:

```sh
busyctl status
busyctl clear apple-music
busyctl clear --all
```

USB uses `10.0.4.20` by default. For Wi-Fi:

```sh
./bin/busyctl apple-music --host 192.168.1.50 --token 1234
```

Environment variables `BUSYBAR_HOST` and `BUSYBAR_TOKEN` are also supported.

### Apple Music

The Apple Music app polls `https://matteing.com/api/now-playing` every 10 seconds, crops the current album artwork, and displays it with the firmware's compact bundled fonts. Long song and artist names use the BUSY Bar's native high-speed scrolling animation.

When playback is stopped or paused, the display becomes a seamless 15 FPS row of up to eight recently played album covers. The covers are precomposed into one cyclic strip with baked subpixel frames and rendered through three stable tile slots, preventing stale wrap fragments. Tune its travel time per pixel with `--recent-speed` (the default is `100ms`, or 10 pixels per second; lower is faster).

Useful options:

```sh
./bin/busyctl apple-music --help
./bin/busyctl apple-music --demo
./bin/busyctl apple-music --once --keep-display
```

`--demo` uses the first recent album when nothing is currently playing, which is useful for display testing.

Only one row scrolls at a time: title, rest, artist, rest, then repeat. The default native scroll speed is 1500 pixels per minute (25 pixels per second), with six seconds per moving row and a three-second rest. Adjust the carousel without rebuilding:

```sh
./bin/busyctl apple-music --scroll-rate 1200 --scroll-time 7s --scroll-rest 4s
```

While a track is playing, rotate the BUSY Bar dial to switch the right side between the song text and a smooth 15 FPS spectrum visualizer. The selected view stays active until the dial is turned again. Its one-pixel frequency bins use a seamless three-second bank of gentle bass pulses and independently drifting mids and highs, colored with a light-to-dark gradient generated from the track's `dominantColor`. The visualizer and album cover share the same 14-row box, leaving one row of padding above and below.

### Hacker News

The Hacker News app shows the current top stories in rotating groups of three. A custom compact bitmap font keeps all three headlines visible at once; longer titles move as independent tickers beside an orange-gradient HN tile with animated pixel sparks.

It refreshes the official Hacker News API every five minutes and keeps the top nine stories in rotation:

```sh
./bin/busyctl hacker-news
./bin/busyctl hacker-news --poll 2m --page-time 12s
./bin/busyctl hacker-news --once --keep-display
```

## Add another app

Create a package under `internal/apps/<name>` with a `Run(context.Context, []string) error` entry point and exported application ID, then add it to the registry in `cmd/busyctl/main.go`. Shared device operations belong in `internal/busybar`; shared media transformations belong in `internal/media`.

## Contributing and releases

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidance and
[SECURITY.md](SECURITY.md) for private vulnerability reporting.

Maintainers publish a macOS-only release by pushing a semantic version tag:

```sh
git tag v0.3.0
git push origin v0.3.0
```

GitHub Actions tests the tag, builds Apple Silicon and Intel archives, creates
checksums, and publishes a GitHub Release with generated release notes.

busyctl is available under the [MIT License](LICENSE).
