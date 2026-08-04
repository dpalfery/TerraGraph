#!/bin/sh
# TerraGraph installer.
#
#   curl -fsSL https://raw.githubusercontent.com/dpalfery/TerraGraph/main/install.sh | sh
#
# Downloads the release archive for this platform, verifies its SHA-256 against the
# published checksums file, and installs both binaries.
#
# Environment:
#   TERRAGRAPH_VERSION      tag to install (default: latest release)
#   TERRAGRAPH_INSTALL_DIR  where to put the binaries (default: first writable of
#                           /usr/local/bin, $HOME/.local/bin)
#
# POSIX sh on purpose: this runs before anything is installed, so it cannot assume bash.

set -eu

REPO="dpalfery/TerraGraph"
BINARIES="terragraph terragraph-mcp"

log()  { printf '%s\n' "$*" >&2; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

# ---------------------------------------------------------------- platform

detect_platform() {
    os="$(uname -s)"
    arch="$(uname -m)"

    case "$os" in
        Darwin) os="darwin" ;;
        Linux)  os="linux" ;;
        *)      die "unsupported operating system: $os (this installer covers macOS and Linux; \
Windows builds are attached to each GitHub release)" ;;
    esac

    case "$arch" in
        x86_64|amd64)  arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)             die "unsupported architecture: $arch" ;;
    esac

    printf '%s_%s' "$os" "$arch"
}

# ---------------------------------------------------------------- download

http_get() {
    # Follow redirects and fail loudly on a 404, so a missing asset is an error rather
    # than an HTML error page silently written to the output file.
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        die "curl or wget is required"
    fi
}

http_body() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    else
        wget -qO- "$1"
    fi
}

latest_version() {
    # The redirect from /releases/latest carries the tag, which avoids needing a JSON
    # parser and avoids the lower rate limit on the API host.
    if command -v curl >/dev/null 2>&1; then
        url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
            "https://github.com/$REPO/releases/latest" 2>/dev/null || true)"
    else
        url="$(wget -qS --max-redirect=10 -O /dev/null \
            "https://github.com/$REPO/releases/latest" 2>&1 |
            awk '/^  Location: /{print $2}' | tail -n1 || true)"
    fi

    case "$url" in
        */tag/*) printf '%s' "${url##*/tag/}" ;;
        *)       return 1 ;;
    esac
}

# ---------------------------------------------------------------- verify

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        return 1
    fi
}

verify_checksum() {
    archive="$1"; checksums="$2"; name="$3"

    actual="$(sha256_of "$archive")" || {
        warn "no sha256sum or shasum available; skipping integrity check"
        return 0
    }

    # The checksums file is produced by `sha256sum ./*`, so entries look like
    # "<hash>  ./terragraph_0.0.1_linux_amd64.tar.gz".
    expected="$(awk -v n="$name" '$2 == n || $2 == "./" n {print $1}' "$checksums" | head -n1)"

    [ -n "$expected" ] || die "no checksum published for $name; refusing to install"

    if [ "$actual" != "$expected" ]; then
        die "checksum mismatch for $name
  expected $expected
  actual   $actual
Refusing to install. This may mean a corrupted download, or a tampered release."
    fi
    log "  checksum ok"
}

# ---------------------------------------------------------------- install dir

choose_install_dir() {
    if [ -n "${TERRAGRAPH_INSTALL_DIR:-}" ]; then
        printf '%s' "$TERRAGRAPH_INSTALL_DIR"
        return
    fi
    if [ -w /usr/local/bin ] 2>/dev/null; then
        printf '%s' /usr/local/bin
        return
    fi
    printf '%s' "$HOME/.local/bin"
}

on_path() {
    case ":${PATH}:" in
        *":$1:"*) return 0 ;;
        *)        return 1 ;;
    esac
}

# ---------------------------------------------------------------- main

main() {
    need uname
    need tar

    platform="$(detect_platform)"

    version="${TERRAGRAPH_VERSION:-}"
    if [ -z "$version" ]; then
        version="$(latest_version)" ||
            die "could not determine the latest release. Set TERRAGRAPH_VERSION=v0.0.1 to pin one."
    fi
    bare="${version#v}"

    name="terragraph_${bare}_${platform}.tar.gz"
    base="https://github.com/$REPO/releases/download/$version"

    log "TerraGraph $version ($platform)"

    tmp="$(mktemp -d)"
    # Clean up on any exit path, including the failure paths above this line's successors.
    trap 'rm -rf "$tmp"' EXIT INT TERM

    log "  downloading $name"
    http_get "$base/$name" "$tmp/$name" ||
        die "could not download $base/$name
Check that $version has an asset for $platform."

    http_get "$base/checksums.txt" "$tmp/checksums.txt" ||
        die "could not download checksums.txt for $version; refusing to install unverified binaries"

    verify_checksum "$tmp/$name" "$tmp/checksums.txt" "$name"

    tar -xzf "$tmp/$name" -C "$tmp"

    dir="$(choose_install_dir)"
    mkdir -p "$dir" || die "cannot create $dir"
    [ -w "$dir" ] || die "$dir is not writable.
Re-run with TERRAGRAPH_INSTALL_DIR=\$HOME/.local/bin, or with sudo."

    for b in $BINARIES; do
        [ -f "$tmp/$b" ] || die "$b missing from the archive"
        install -m 0755 "$tmp/$b" "$dir/$b" 2>/dev/null ||
            { cp "$tmp/$b" "$dir/$b" && chmod 0755 "$dir/$b"; }
        log "  installed $dir/$b"
    done

    log ""
    if on_path "$dir"; then
        log "Done. Try:"
        log "  terragraph status --repo /path/to/your/terraform"
    else
        log "Done, but $dir is not on your PATH. Add it:"
        log "  export PATH=\"$dir:\$PATH\""
        log ""
        log "Then try:"
        log "  terragraph status --repo /path/to/your/terraform"
    fi
    log ""
    log "To register the MCP server with an agent:"
    log "  claude mcp add terragraph -- $dir/terragraph-mcp --repo /path/to/your/terraform"
}

main "$@"
