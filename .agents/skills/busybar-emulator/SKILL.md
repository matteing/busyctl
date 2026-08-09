---
name: busybar-emulator
description: Run and manage the local BUSY Bar emulator for this busyctl repository. Use when Codex needs to build, debug, preview, animate, profile, or visually QA a BUSY Bar app; exercise display priority, disconnect, power, input, or asset behavior without touching physical hardware; or prepare a stable emulator frame before a README screenshot. Use busybar-screenshots for the canonical transparent device-shell capture.
---

# BUSY Bar Emulator

Develop against `127.0.0.1:8080`. Default to the emulator for iteration. Never
target physical hardware or enable mirroring unless the user explicitly asks.

Read [references/emulator-api.md](references/emulator-api.md) before using
scenario endpoints, hardware mirroring, input, raw PNG/GIF capture, or direct
display requests.

## Start safely

Run the bundled manager in an ongoing terminal session and retain its session
ID:

```sh
.agents/skills/busybar-emulator/scripts/emulator.sh serve
```

Use `status` first when the emulator may already be running. `serve` installs
the upstream emulator when missing and keeps it in the foreground so the tool
runner cannot reap it.

Before drawing anything, require both checks to pass:

```sh
curl -fsS http://127.0.0.1:8080/api/version
curl -fsS http://127.0.0.1:8080/api/_mirror
```

The mirror response must contain `"enabled":false`. If it is enabled, stop
before drawing. Do not change saved mirror settings without explicit user
authorization. If the user authorizes disabling relay, use:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/_mirror \
  -H 'content-type: application/json' \
  -d '{"enabled":false}'
```

Read `/api/_mirror` again and verify the disabled state before continuing. Do
not print, copy, or clear a saved hardware token. Localhost needs no token.

Reset scenarios and the emulated display before a clean run:

```sh
.agents/skills/busybar-emulator/scripts/emulator.sh reset
```

## Run the current tree

Build or run the working tree, not a previously installed `busyctl`. Override
only the device host; never change the production default host for testing.

Isolate every interactive preview from busyctl's normal single-instance state.
A non-isolated preview can stop the user's live app:

```sh
capture_instance_dir="$(mktemp -d /tmp/busyctl-emulator.XXXXXX)"
env BUSYCTL_INSTANCE_DIR="$capture_instance_dir" BUSYBAR_TOKEN= \
  go run ./cmd/busyctl \
  --host 127.0.0.1:8080 \
  --keep-display \
  minecraft 127.0.0.1:25566 --poll 1h
```

Use a localhost fixture when the app depends on changing, private, or remote
data. Keep secrets and personal data out of commands, logs, captures, and Git.
Do not kill or take over another `busyctl` process.

Wait for the app's connected/ready output and the first successful refresh.
Keep the process observable while inspecting animation or error behavior. Send
Ctrl+C to the isolated preview process; `--keep-display` retains its final
frame for inspection.

## Verify behavior

- Inspect the live LED canvas for margins, clipping, font rendering, scrolling,
  gamma, asset transitions, stale elements, loop seams, and frame pacing.
- Use the emulator PNG control only for a raw 720x160 display export. Use GIF
  for animation pacing and looping.
- Exercise priority, temporary disconnect, power, and input behavior only when
  relevant. Reset scenarios immediately afterward.
- Treat emulator behavior as visual and protocol evidence, not full firmware
  proof. Request a physical-device smoke test only when hardware-only behavior
  materially matters.
- Run `go test ./...`, `go vet ./...`, and `go build ./...` after app changes.

For a README product shot, hand the stable frame to
[the BUSY Bar screenshot workflow](../busybar-screenshots/SKILL.md). Do not use
the raw PNG control for those images.

## Clean up

1. Stop temporary app and fixture processes with Ctrl+C.
2. Reset scenarios and display state.
3. Stop the foreground emulator with Ctrl+C in its original session.
4. Remove only the exact temporary instance directory created for the run.
5. Close or finalize temporary browser tabs.

Never terminate an arbitrary process merely because it owns port 8080. The
manager's `stop` command refuses to kill a process it does not own.
