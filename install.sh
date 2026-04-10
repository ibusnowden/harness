#!/usr/bin/env bash
set -euo pipefail

if [ -t 1 ] && command -v tput >/dev/null 2>&1 && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
    COLOR_RESET="$(tput sgr0)"
    COLOR_BOLD="$(tput bold)"
    COLOR_GREEN="$(tput setaf 2)"
    COLOR_YELLOW="$(tput setaf 3)"
    COLOR_BLUE="$(tput setaf 4)"
    COLOR_CYAN="$(tput setaf 6)"
else
    COLOR_RESET=""
    COLOR_BOLD=""
    COLOR_GREEN=""
    COLOR_YELLOW=""
    COLOR_BLUE=""
    COLOR_CYAN=""
fi

step() {
    printf '\n%s%s%s\n' "${COLOR_BLUE}" "$1" "${COLOR_RESET}"
}

info() {
    printf '%s  ->%s %s\n' "${COLOR_CYAN}" "${COLOR_RESET}" "$1"
}

ok() {
    printf '%s  ok%s %s\n' "${COLOR_GREEN}" "${COLOR_RESET}" "$1"
}

warn() {
    printf '%s  warn%s %s\n' "${COLOR_YELLOW}" "${COLOR_RESET}" "$1"
}

print_usage() {
    cat <<'EOF'
Usage: ./install.sh [options]

Options:
  --release       Build an optimized release binary.
  --debug         Build a normal development binary (default).
  --no-verify     Skip post-build smoke checks.
  -h, --help      Show this help text.
EOF
}

BUILD_MODE="debug"
VERIFY="1"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --release)
            BUILD_MODE="release"
            ;;
        --debug)
            BUILD_MODE="debug"
            ;;
        --no-verify)
            VERIFY="0"
            ;;
        -h|--help)
            print_usage
            exit 0
            ;;
        *)
            printf 'unknown argument: %s\n' "$1" 1>&2
            print_usage
            exit 2
            ;;
    esac
    shift
done

step "Checking Go toolchain"
if ! command -v go >/dev/null 2>&1; then
    printf 'Go is required but was not found in PATH.\n' 1>&2
    exit 1
fi
info "$(go version)"

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
BIN_PATH="${BIN_DIR}/ascaris"
CACHE_DIR="${ROOT_DIR}/.cache/go-build"

mkdir -p "${BIN_DIR}"
mkdir -p "${CACHE_DIR}"

cd "${ROOT_DIR}"

step "Building ascaris"
info "root=${ROOT_DIR}"
info "gocache=${GOCACHE:-${CACHE_DIR}}"
if [ "${BUILD_MODE}" = "release" ]; then
    info "mode=release"
    GOCACHE="${GOCACHE:-${CACHE_DIR}}" go build -trimpath -ldflags="-s -w" -o "${BIN_PATH}" ./cmd/ascaris
else
    info "mode=debug"
    GOCACHE="${GOCACHE:-${CACHE_DIR}}" go build -o "${BIN_PATH}" ./cmd/ascaris
fi
ok "built ${BIN_PATH}"

if [ "${VERIFY}" = "1" ]; then
    step "Running smoke checks"
    if "${BIN_PATH}" doctor >/dev/null; then
        ok "ascaris doctor responded"
    else
        printf 'ascaris doctor failed\n' 1>&2
        exit 1
    fi
    if "${BIN_PATH}" status --json >/dev/null; then
        ok "ascaris status --json responded"
    else
        printf 'ascaris status --json failed\n' 1>&2
        exit 1
    fi
else
    warn "verification skipped"
fi

step "Done"
printf '%sAscaris is built and ready.%s\n' "${COLOR_BOLD}" "${COLOR_RESET}"
printf '  Binary: %s\n' "${BIN_PATH}"
printf '  Try:    %s doctor\n' "${BIN_PATH}"
