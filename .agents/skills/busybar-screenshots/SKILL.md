---
name: busybar-screenshots
description: Capture and refresh this repository's canonical BUSY Bar README product shots. Use when Codex needs to add or retake an image under docs/screenshots, document a rendering change, replace a raw emulator PNG with the transparent device-shell treatment, or visually verify a BUSY Bar app exactly as it appears in the established README assets. Produces deterministic 520x180 RGBA PNGs from the rendered emulator page, not raw 720x160 matrix exports or hand-built shell composites.
---

# BUSY Bar Screenshots

Create the same product-shot treatment as commits `34a7557` and `6834700`:
the real emulator-rendered shell and knobs, centered on a 520x180 transparent
canvas, with the black LED face intact.

Use [the emulator workflow](../busybar-emulator/SKILL.md) first. Read
[references/capture-spec.md](references/capture-spec.md) for fixture commands,
geometry, alpha details, and the acceptance contract.

## Render deterministic state

1. Start the repo-local emulator in the foreground.
2. Verify `/api/version` and require `/api/_mirror` to report
   `"enabled":false` before any draw.
3. Build `./cmd/busyctl` from the current tree into a fresh temporary directory.
4. Use a fresh `BUSYCTL_INSTANCE_DIR`, `BUSYBAR_TOKEN=`, localhost fixtures,
   and `--keep-display`. Never take over or stop the user's live busyctl app.
5. Wait for connected/ready output and a complete refresh. For animation,
   deliberately select a readable, balanced frame.
6. Send Ctrl+C to the isolated preview so the chosen frame remains stable.

Use the bundled fixture servers for the canonical Muni and Minecraft states.
For another network-, time-, or account-dependent app, create an equivalent
stable, synthetic, non-personal localhost fixture.

## Capture the rendered product shell

Use the in-app Browser or another screenshot-capable Chromium surface. Set its
viewport to exactly 1280x720, open `http://127.0.0.1:8080/`, wait for
`domcontentloaded`, and visually inspect the complete shell.

Capture the whole viewport with `fullPage:false` and save the returned bytes to
a temporary JPEG or PNG. Verify that the full frame is 1280x720.

Do not click the emulator's PNG button. It exports only the raw 720x160 matrix.
Do not manually composite `busybar-device.png`; that misses the browser's CSS
sizing, filtering, and antialiasing.

Convert the full browser frame with the bundled wrapper. It selects an explicit
`BUSYBAR_SCREENSHOT_PYTHON`, the cache-relative Codex workspace runtime, or an
existing `python3` with Pillow 12.2 or newer, in that order. This also works in
child agents that do not expose the workspace-dependency loader.

When available, `codex_app__load_workspace_dependencies({})` (shown as
`load_workspace_dependencies` in some tool lists) remains the authoritative
way to find the runtime; pass its Python path through
`BUSYBAR_SCREENSHOT_PYTHON`.

Do not invoke Nix, Homebrew, or a system-wide package install to satisfy this
dependency. Do not accept a multi-gigabyte environment download. Outside Codex
Desktop, use an already-available Python with Pillow; if none exists, stop and
report the missing dependency rather than changing the machine.

Run:

```sh
.agents/skills/busybar-screenshots/scripts/product_shot.sh \
  prepare /tmp/busybar-full.png docs/screenshots/APP.png
```

The tool enforces the exact crop and historical transparency algorithm, then
validates the output contract.

## Verify on transparency

Run programmatic verification and generate a checkerboard QA image:

```sh
.agents/skills/busybar-screenshots/scripts/product_shot.sh \
  verify docs/screenshots/APP.png

.agents/skills/busybar-screenshots/scripts/product_shot.sh \
  checkerboard /tmp/busybar-screenshot-qa.png docs/screenshots/APP.png
```

Inspect both the final PNG and checkerboard at original size. Require:

- 520x180 RGBA PNG with transparent corners and a feathered alpha edge;
- centered, complete emulator shell with both knobs and no editor chrome;
- no opaque rectangular background, dark exterior pockets, or clipped shadow;
- intact black display face and correct app text, counts, colors, and status;
- an intentional animation frame with no partial upload or stale element.

Compare shell and alpha bounds with an existing canonical screenshot if the
browser or emulator version changed. Do not silently accept geometry drift.

## Update the README

Store lowercase kebab-case assets in `docs/screenshots/` and use centered markup:

```html
<p align="center">
  <img src="docs/screenshots/APP.png" alt="APP on the BUSY Bar" width="520">
</p>
```

Show the final screenshot inline in the handoff. Run `go test ./...`,
`go vet ./...`, `go build ./...`, and `git diff --check` when the capture
documents code changes.

## Clean up completely

Finalize browser tabs as the last browser action. Stop the isolated app,
fixture, and foreground emulator through their retained sessions. Reset the
emulator, remove only the exact temporary files/directories created for the
capture, and verify no capture listener remains. Preserve unrelated processes
and dirty worktree changes.
