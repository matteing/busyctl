# BUSY Bar Apps

A single, self-contained Go binary for running host-side apps on a BUSY Bar. Apps use the release firmware HTTP API; they do not install custom firmware.

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

Useful options:

```sh
./bin/busyctl apple-music --help
./bin/busyctl apple-music --demo
./bin/busyctl apple-music --once --keep-display
```

`--demo` uses the first recent album when nothing is currently playing, which is useful for display testing.

The default native scroll speed is 1500 pixels per minute (25 pixels per second). Adjust it without rebuilding:

```sh
./bin/busyctl apple-music --scroll-rate 1200
```

## Add another app

Create a package under `internal/apps/<name>` with a `Run(context.Context, []string) error` entry point and exported application ID, then add it to the registry in `cmd/busyctl/main.go`. Shared device operations belong in `internal/busybar`; shared media transformations belong in `internal/media`.
