# Emulator API and QA reference

Prefer the running emulator's `/docs` and `/openapi.json` when its checkout has
changed.

## Connection

- Web UI: `http://127.0.0.1:8080/`
- REST API: `http://127.0.0.1:8080/api`
- Health: `GET /api/version`
- Swagger: `http://127.0.0.1:8080/docs`
- OpenAPI: `http://127.0.0.1:8080/openapi.json`
- Front display: 72x16 LEDs, gamma approximation `0.35`

Pass busyctl a host without a URL scheme:

```sh
go run ./cmd/busyctl --host 127.0.0.1:8080 clock
```

## Display endpoints

| Request | Purpose |
| --- | --- |
| `POST /api/display/draw` | Draw `{application_name, priority, elements}` |
| `DELETE /api/display/draw?application_name=NAME` | Clear one composition |
| `DELETE /api/display/draw` | Clear every composition |
| `POST /api/assets/upload?application_name=NAME&file=PATH` | Upload PNG bytes |
| `DELETE /api/assets/upload?application_name=NAME` | Delete one app's assets |
| `POST /api/input?key=KEY` | Simulate hardware input |

Priorities range from 1 through 100. The current owner may redraw at equal
priority. A different app needs a strictly higher priority or receives 409.

## Scenario testing

Always reset after a scenario:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/_scenario/reset
```

Power state:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/_scenario/power \
  -H 'content-type: application/json' \
  -d '{"battery_charge":12,"state":"discharging"}'
```

Temporary disconnect:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/_scenario/offline \
  -H 'content-type: application/json' \
  -d '{"duration_ms":3000}'
```

Priority stealing:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/_scenario/steal \
  -H 'content-type: application/json' \
  -d '{"priority":99,"duration_ms":3000}'
```

## Input and raw captures

Inspect the app or live OpenAPI schema before selecting an input key:

```sh
curl -fsS -X POST \
  'http://127.0.0.1:8080/api/input?key=rotate_clockwise'
```

The web UI's PNG button exports only the raw matrix at 720x160 (10x scale).
GIF records the raw matrix at 20 FPS for up to 30 seconds. Neither output is a
canonical README product shot.

## Hardware mirror

The emulator can relay draw, clear, brightness, audio, and asset requests to a
real BUSY Bar. Treat this as a hardware write.

- `GET /api/_mirror` reports configuration without returning the token.
- `POST /api/_mirror` with `{"enabled":false}` disables relay while retaining
  the configured host and token.
- `POST /api/_mirror/test` probes hardware without persisting a new setting.

Never enable or test a mirror without explicit user authorization. Never log or
commit the real token.

## Fidelity limits

Storage, smart-home, Wi-Fi, update, BLE, back-display, and some audio behavior
are simplified. Same-app merging, per-element timeout behavior, and the real
device's approximately 1.5-second priority-conflict delay are not fully
emulated.
