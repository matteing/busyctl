# BUSY Bar Apps

A single, self-contained Go binary for running host-side apps on a BUSY Bar. Apps use the release firmware HTTP API; they do not install custom firmware.

## Toolchain

The project pins Go with `mise`:

```sh
mise install
```

## Build

```sh
mise exec -- go build -o bin/busybar ./cmd/busybar
```

The resulting `bin/busybar` file has no runtime dependencies.

## Use

Run without arguments for an interactive app selector:

```sh
./bin/busybar
```

Or select an app directly:

```sh
./bin/busybar apps
./bin/busybar apple-music
```

USB uses `10.0.4.20` by default. For Wi-Fi:

```sh
./bin/busybar apple-music --host 192.168.1.50 --token 1234
```

Environment variables `BUSYBAR_HOST` and `BUSYBAR_TOKEN` are also supported.

### Apple Music

The Apple Music app polls `https://matteing.com/api/now-playing` every 10 seconds, crops the current album artwork, and displays it with the firmware's compact bundled fonts. Long song and artist names use the BUSY Bar's native high-speed scrolling animation.

Useful options:

```sh
./bin/busybar apple-music --help
./bin/busybar apple-music --demo
./bin/busybar apple-music --once --keep-display
```

`--demo` uses the first recent album when nothing is currently playing, which is useful for display testing.

The default native scroll speed is 1500 pixels per minute (25 pixels per second). Adjust it without rebuilding:

```sh
./bin/busybar apple-music --scroll-rate 1200
```

## Add another app

Create a package under `internal/apps/<name>` with a `Run(context.Context, []string) error` entry point, then add it to the registry in `cmd/busybar/main.go`. Shared device operations belong in `internal/busybar`; shared media transformations belong in `internal/media`.
