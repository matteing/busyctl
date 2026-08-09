#!/usr/bin/env bash
set -euo pipefail

repo_url="https://github.com/maxswinkels/busybar-emulator.git"
data_root="${XDG_DATA_HOME:-${HOME}/.local/share}"
emulator_dir="${BUSYBAR_EMULATOR_DIR:-${data_root}/busybar-emulator}"
runtime_root="${BUSYBAR_EMULATOR_RUNTIME_DIR:-${TMPDIR:-/tmp}/busybar-emulator}"
emulator_port="${BUSYBAR_EMULATOR_PORT:-8080}"
pid_file="${runtime_root}/server-${emulator_port}.pid"
log_file="${runtime_root}/server-${emulator_port}.log"
base_url="http://127.0.0.1:${emulator_port}"

die() {
  printf 'busybar-emulator: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

health() {
  curl --fail --silent --show-error --max-time 2 "${base_url}/api/version"
}

managed_pid() {
  [[ -f "${pid_file}" ]] || return 1
  local emulator_pid emulator_command
  emulator_pid="$(tr -dc '0-9' < "${pid_file}")"
  [[ -n "${emulator_pid}" ]] || return 1
  kill -0 "${emulator_pid}" 2>/dev/null || return 1
  emulator_command="$(ps -p "${emulator_pid}" -o command= 2>/dev/null || true)"
  [[ "${emulator_command}" == *"${emulator_dir}/server.js"* ]] || return 1
  printf '%s\n' "${emulator_pid}"
}

install_emulator() {
  need git
  need node
  need npm

  if [[ -d "${emulator_dir}/.git" ]]; then
    printf 'Using existing checkout: %s\n' "${emulator_dir}"
  elif [[ -e "${emulator_dir}" ]]; then
    die "install target exists but is not an emulator checkout: ${emulator_dir}"
  else
    mkdir -p "$(dirname "${emulator_dir}")"
    git clone --depth 1 "${repo_url}" "${emulator_dir}"
  fi

  npm --prefix "${emulator_dir}/web" ci
  npm --prefix "${emulator_dir}/web" run build
  printf 'Installed emulator in %s\n' "${emulator_dir}"
}

ensure_installed() {
  if [[ ! -f "${emulator_dir}/server.js" || ! -f "${emulator_dir}/web/dist/index.html" ]]; then
    install_emulator
  fi
}

update_emulator() {
  [[ -d "${emulator_dir}/.git" ]] || die "emulator is not installed; run install first"
  git -C "${emulator_dir}" pull --ff-only
  install_emulator
}

start_emulator() {
  need curl
  if health >/dev/null 2>&1; then
    printf 'Emulator already reachable at %s\n' "${base_url}"
    health
    printf '\n'
    return
  fi

  ensure_installed
  need node
  mkdir -p "${runtime_root}"

  PORT="${emulator_port}" nohup node "${emulator_dir}/server.js" >"${log_file}" 2>&1 &
  local emulator_pid=$!
  printf '%s\n' "${emulator_pid}" >"${pid_file}"

  local attempt
  for attempt in {1..50}; do
    if health >/dev/null 2>&1; then
      printf 'Started emulator at %s (pid %s)\n' "${base_url}" "${emulator_pid}"
      health
      printf '\nLog: %s\n' "${log_file}"
      return
    fi
    if ! kill -0 "${emulator_pid}" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done

  printf 'Emulator failed to become ready. Recent log output:\n' >&2
  tail -n 40 "${log_file}" >&2 || true
  return 1
}

serve_emulator() {
  need curl
  if health >/dev/null 2>&1; then
    die "an emulator is already reachable at ${base_url}"
  fi
  ensure_installed
  need node
  printf 'Serving emulator in the foreground at %s; press Ctrl+C to stop.\n' "${base_url}"
  cd "${emulator_dir}"
  exec env PORT="${emulator_port}" node "${emulator_dir}/server.js"
}

stop_emulator() {
  local emulator_pid
  if ! emulator_pid="$(managed_pid)"; then
    if health >/dev/null 2>&1; then
      die "an emulator is reachable at ${base_url}, but it is not owned by this manager"
    fi
    rm -f "${pid_file}"
    printf 'Emulator is not running\n'
    return
  fi

  kill -TERM "${emulator_pid}"
  local attempt
  for attempt in {1..30}; do
    if ! kill -0 "${emulator_pid}" 2>/dev/null; then
      rm -f "${pid_file}"
      printf 'Stopped emulator (pid %s)\n' "${emulator_pid}"
      return
    fi
    sleep 0.1
  done
  die "emulator did not stop after SIGTERM; inspect ${log_file}"
}

status_emulator() {
  if health; then
    printf '\nDisplay: %s/\nDocs: %s/docs\n' "${base_url}" "${base_url}"
    return
  fi
  printf 'Emulator is not reachable at %s\n' "${base_url}" >&2
  return 1
}

reset_emulator() {
  need curl
  health >/dev/null || die "emulator is not reachable at ${base_url}"
  curl --fail --silent --show-error -X POST "${base_url}/api/_scenario/reset" >/dev/null
  curl --fail --silent --show-error -X DELETE "${base_url}/api/display/draw" >/dev/null
  printf 'Reset scenarios and display at %s\n' "${base_url}"
}

usage() {
  cat <<'EOF'
Usage: emulator.sh <command>

Commands:
  install   Clone when needed, install web dependencies, and build the UI
  update    Fast-forward the checkout and rebuild the UI
  serve     Run in the foreground (preferred in Codex tool sessions)
  start     Start in the background and wait for API readiness
  stop      Stop only the server started by this manager
  restart   Stop, then start
  status    Check /api/version and print display/docs URLs
  reset     Reset scenarios and clear the emulated display
  logs      Print recent server logs
  url       Print the emulator base URL

Environment:
  BUSYBAR_EMULATOR_DIR          Checkout location
  BUSYBAR_EMULATOR_PORT         Server port (default 8080)
  BUSYBAR_EMULATOR_RUNTIME_DIR  PID/log directory
  BUSY_API_TOKEN                Optional LAN API token used by upstream server
EOF
}

command_name="${1:-}"
case "${command_name}" in
  install) install_emulator ;;
  update) update_emulator ;;
  serve) serve_emulator ;;
  start) start_emulator ;;
  stop) stop_emulator ;;
  restart) stop_emulator; start_emulator ;;
  status) need curl; status_emulator ;;
  reset) reset_emulator ;;
  logs) tail -n 100 "${log_file}" ;;
  url) printf '%s\n' "${base_url}" ;;
  -h|--help|help) usage ;;
  "") usage; exit 2 ;;
  *) usage >&2; die "unknown command: ${command_name}" ;;
esac
