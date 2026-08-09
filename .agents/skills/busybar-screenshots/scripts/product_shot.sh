#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cache_root="${XDG_CACHE_HOME:-${HOME}/.cache}"
workspace_python="${cache_root}/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3"

candidates=()
if [[ -n "${BUSYBAR_SCREENSHOT_PYTHON:-}" ]]; then
  candidates+=("${BUSYBAR_SCREENSHOT_PYTHON}")
fi
candidates+=("${workspace_python}" "python3")

for candidate in "${candidates[@]}"; do
  if [[ "${candidate}" == */* ]]; then
    [[ -x "${candidate}" ]] || continue
  elif ! command -v "${candidate}" >/dev/null 2>&1; then
    continue
  fi
  if "${candidate}" -c 'import PIL; major, minor = map(int, PIL.__version__.split(".")[:2]); raise SystemExit(0 if (major, minor) >= (12, 2) else 1)' >/dev/null 2>&1; then
    exec "${candidate}" "${script_dir}/product_shot.py" "$@"
  fi
done

printf '%s\n' \
  'product_shot.sh: Pillow 12.2+ was not found.' \
  'In Codex Desktop, use load_workspace_dependencies and set:' \
  '  BUSYBAR_SCREENSHOT_PYTHON=/absolute/path/to/python3' \
  'Do not install a heavyweight Nix, Homebrew, or system environment.' >&2
exit 2
