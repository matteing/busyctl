# Canonical capture specification

- [Artifact contract](#artifact-contract)
- [Safety and isolation](#safety-and-isolation)
- [Canonical Muni frame](#canonical-muni-frame)
- [Canonical Minecraft frame](#canonical-minecraft-frame)
- [Browser and crop geometry](#browser-and-crop-geometry)
- [Transparency algorithm](#transparency-algorithm)

## Artifact contract

- Path: `docs/screenshots/<lowercase-kebab>.png`
- Format: PNG, RGBA, exactly 520x180
- Content: rendered emulator shell and knobs, not the raw matrix
- Alpha bbox: `(38, 19, 482, 171)` with right/bottom exclusive
- Corners: alpha 0
- Expected alpha classes at the established versions: 31,042 transparent,
  approximately 7,000 partial, and approximately 55,550 opaque pixels
- README display width: 520

Use `scripts/product_shot.sh`; it resolves an explicit
`BUSYBAR_SCREENSHOT_PYTHON`, the cache-relative Codex workspace runtime, or an
existing Pillow-capable `python3`. The validated runtime includes Pillow
12.2.0. Never substitute a large Nix/Homebrew installation during capture.

If dependency versions change these metrics, compare against another tracked
screenshot and visually review the checkerboard. Never accept drift silently.

## Safety and isolation

1. Run `.agents/skills/busybar-emulator/scripts/emulator.sh serve` in a retained
   foreground session.
2. Require `GET /api/_mirror` to show `enabled:false`. If enabled, stop before
   drawing; do not alter saved mirror configuration or touch hardware without
   explicit user authorization.
3. Build the current tree into a fresh temporary directory. Do not use the
   installed PATH binary.
4. Set `BUSYCTL_INSTANCE_DIR` to a fresh directory for every preview. This
   prevents single-instance takeover from stopping the user's live app.
5. Set `BUSYBAR_TOKEN=` and use loopback-only app sources.

Example setup:

```sh
capture_root="$(mktemp -d /tmp/busybar-shot.XXXXXX)"
mkdir "$capture_root/instance"
go build -o "$capture_root/busyctl" ./cmd/busyctl
```

## Canonical Muni frame

Start the bundled fixture in a retained session:

```sh
python3 .agents/skills/busybar-screenshots/scripts/muni_fixture.py --port 18081
```

Then run:

```sh
env BUSYCTL_INSTANCE_DIR="$capture_root/instance" \
  BUSYBAR_TOKEN= SFMTA_API_KEY=fixture \
  "$capture_root/busyctl" \
  --host 127.0.0.1:8080 --keep-display \
  muni to-openai --source http://127.0.0.1:18081
```

Wait for `N: 4m to Caltrain/Ballpark` and `T: 7m to Sunnydale`. The visible
rows must read `N  Caltrain  4m` and `T  Sunnydale  7m` with full labels and
natural-width right-aligned ETAs. Stop the isolated app with Ctrl+C.

## Canonical Minecraft frame

Start the bundled Java-status fixture in a retained session:

```sh
python3 .agents/skills/busybar-screenshots/scripts/minecraft_fixture.py \
  --port 25566
```

Then run:

```sh
env BUSYCTL_INSTANCE_DIR="$capture_root/instance" BUSYBAR_TOKEN= \
  "$capture_root/busyctl" \
  --host 127.0.0.1:8080 --keep-display \
  minecraft 127.0.0.1:25566 --poll 1h
```

Require `ONLINE` and `2 / 5`. Choose a bright, readable creeper wave/sparkle
frame, then stop the isolated app with Ctrl+C to freeze it.

## Browser and crop geometry

- Browser viewport: 1280x720
- Screenshot: whole viewport, `fullPage:false`
- Crop origin: x=380, y=75
- Crop size: 520x180
- Shell CSS width: 440px
- Shell placement in crop: approximately x=40, y=27
- LED matrix bounds in crop: approximately x=54.3..465.7,
  y=62.1..153.5

The historical macOS command was:

```sh
sips --cropToHeightWidth 180 520 --cropOffset 75 380 \
  --setProperty format png FULL_FRAME --out OPAQUE_CROP
```

`product_shot.py` performs the equivalent exact pixel crop portably.

## Transparency algorithm

1. Convert the opaque crop to RGBA.
2. Mark a pixel as neutral exterior when `max(r,g,b) <= 38` and channel spread
   is at most 10.
3. Seed every matching border pixel and flood-fill matching 4-neighbors.
4. Also mark matching pixels in rows 0 through 51 to clear pockets around the
   top controls.
5. Build an L mask with exterior 0 and retained pixels 255.
6. Apply `GaussianBlur(0.55)` to reproduce the established feather.
7. Replace alpha with the mask and save an optimized PNG.
