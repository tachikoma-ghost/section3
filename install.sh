#!/bin/sh
# section3 installer.
#
#   curl -fsSL https://signalshell.com/install-section3 | sh
#
# TWO COPIES, KEPT BYTE-IDENTICAL: this file, and `install-section3` in the
# signalshell-landing repo, which is what the URL above actually serves.
# `make check-installer` diffs them; the release also publishes this one to
# releases/section3/install.sh. Edit here, copy there.
#
# Why it was rewritten (2026-09-07). The installer it replaces verified a
# sha256 that it fetched from the same server as the binary, which catches a
# corrupted download and nothing else: anyone able to serve the binary can
# serve a matching hash. The minisign signature that `section3 self update`
# has always checked was published beside every release and never used here.
# Worse, with no sha256 tool present the old script printed a warning and then
# assigned the expected hash to the actual one, so the comparison passed and
# an entirely unverified binary was installed.
#
# So: same key, same two algorithms, as the Go verifier in selfupdate.go. This
# script will NOT install an unverified binary. If nothing on the machine can
# check a signature it stops and says what to install, rather than warning and
# continuing.
#
# Env:
#   SECTION3_INSTALL_DIR   where to put the binary (default: see pick_dir)
#   SECTION3_VERSION       install this version instead of the latest
set -eu

BASE_URL="https://signalshell.com/releases/section3"
# Key ID 4553E564F8700D47. Same constant as releasePublicKey in selfupdate.go;
# if one changes the other must.
PUBKEY="RWRHDXD4ZOVTRfvfv/shVjvlkOBGp3OxN+KILl6yDWY20SByxOQP/OnO"

die() { echo "install.sh: $*" >&2; exit 1; }
say() { echo "==> $*"; }

need() { command -v "$1" >/dev/null 2>&1; }

# --- platform -------------------------------------------------------------
detect_platform() {
    # Only what the release actually builds (PLATFORMS in the Makefile). An
    # installer that offers a platform with no published binary just moves the
    # failure to a confusing 404.
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux) ;;
        darwin) die "no darwin build is published — section3 releases are linux/amd64 and linux/arm64. Build from source: go build ." ;;
        *) die "unsupported OS: $os — section3 releases are linux/amd64 and linux/arm64" ;;
    esac
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) die "unsupported architecture: $arch — section3 releases are amd64 and arm64" ;;
    esac
    echo "${os}-${arch}"
}

# --- destination ----------------------------------------------------------
# Prefer a directory already on PATH and writable, so the install does not
# need root and the binary is runnable immediately. ~/.local/bin is the
# fallback because it is on PATH for most modern shells and is always ours.
pick_dir() {
    if [ -n "${SECTION3_INSTALL_DIR:-}" ]; then
        echo "$SECTION3_INSTALL_DIR"; return
    fi
    for d in /usr/local/bin "$HOME/.local/bin"; do
        if [ -d "$d" ] && [ -w "$d" ]; then echo "$d"; return; fi
    done
    echo "$HOME/.local/bin"
}

# --- verification ---------------------------------------------------------
# Two implementations of one check. minisign is the reference; the openssl
# path exists because minisign is not installed by default anywhere, and
# without it this script would have to either skip verification or refuse to
# run on a normal machine.

verify_with_minisign() {
    # -V verify, -P inline public key, -m message, -x signature.
    minisign -V -P "$PUBKEY" -m "$1" -x "$2" >/dev/null 2>&1
}

verify_with_openssl() {
    bin=$1; sig=$2; work=$3

    # base64 via openssl throughout: this branch already requires it, and the
    # base64(1) flags differ between GNU and macOS.
    # Public key: base64 → alg[2] keyID[8] key[32].
    printf '%s' "$PUBKEY" | openssl base64 -d -A > "$work/pub.raw" 2>/dev/null ||
        die "could not decode the embedded public key"
    [ "$(wc -c < "$work/pub.raw")" -eq 42 ] || die "embedded public key is malformed"
    dd if="$work/pub.raw" bs=1 skip=2 count=8 of="$work/pub.keyid" 2>/dev/null
    dd if="$work/pub.raw" bs=1 skip=10 count=32 of="$work/pub.key" 2>/dev/null

    # Wrap the raw key as an Ed25519 SubjectPublicKeyInfo so openssl will load
    # it. MCowBQYDK2VwAyEA is the fixed 12-byte DER header for id-Ed25519; 12
    # is a multiple of 3, so its base64 concatenates cleanly with the key's.
    {
        echo "-----BEGIN PUBLIC KEY-----"
        printf 'MCowBQYDK2VwAyEA%s\n' "$(openssl base64 -A -in "$work/pub.key")"
        echo "-----END PUBLIC KEY-----"
    } > "$work/pub.pem"
    openssl pkey -pubin -in "$work/pub.pem" -noout 2>/dev/null ||
        die "openssl could not load the public key"

    # Signature file: line 2 is base64 of alg[2] keyID[8] sig[64].
    sed -n '2p' "$sig" | tr -d '\r\n' | openssl base64 -d -A > "$work/sig.raw" 2>/dev/null ||
        die "could not decode the signature file"
    [ "$(wc -c < "$work/sig.raw")" -eq 74 ] || die "signature file is malformed"
    alg=$(dd if="$work/sig.raw" bs=1 count=2 2>/dev/null)
    dd if="$work/sig.raw" bs=1 skip=2 count=8 of="$work/sig.keyid" 2>/dev/null
    dd if="$work/sig.raw" bs=1 skip=10 count=64 of="$work/sig.bin" 2>/dev/null

    cmp -s "$work/pub.keyid" "$work/sig.keyid" ||
        die "key ID mismatch: this signature was not made by the section3 release key"

    # "ED" signs a blake2b-512 prehash, "Ed" signs the file itself. Both are
    # accepted by the Go verifier, so both are accepted here.
    case "$alg" in
        ED) openssl dgst -blake2b512 -binary "$bin" > "$work/msg" ;;
        Ed) cp "$bin" "$work/msg" ;;
        *)  die "unsupported signature algorithm: $alg" ;;
    esac

    openssl pkeyutl -verify -pubin -inkey "$work/pub.pem" \
        -rawin -in "$work/msg" -sigfile "$work/sig.bin" >/dev/null 2>&1
}

verify() {
    if need minisign; then
        say "verifying signature (minisign)"
        verify_with_minisign "$1" "$2" || die "SIGNATURE VERIFICATION FAILED — not installing"
    elif need openssl && need dd && need cmp; then
        say "verifying signature (openssl)"
        verify_with_openssl "$1" "$2" "$3" || die "SIGNATURE VERIFICATION FAILED — not installing"
    else
        die "no way to verify the download on this machine.
  Install minisign (apt install minisign / brew install minisign) or openssl,
  then run this again. This script does not install unverified binaries."
    fi
    say "signature verified"
}

# --- main -----------------------------------------------------------------
need curl || die "curl is required"

platform=$(detect_platform)

if [ -n "${SECTION3_VERSION:-}" ]; then
    version=$SECTION3_VERSION
else
    say "checking $BASE_URL/latest.json"
    manifest=$(curl -fsSL "$BASE_URL/latest.json") || die "could not fetch the release manifest"
    # One integer field; a JSON parser is not worth a dependency here.
    version=$(printf '%s' "$manifest" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p')
    [ -n "$version" ] || die "could not read a version out of: $manifest"
fi

name="section3-${platform}"
url="$BASE_URL/$version/$name"

work=$(mktemp -d) || die "could not create a temp directory"
trap 'rm -rf "$work"' EXIT INT TERM

say "downloading section3 v$version ($platform)"
curl -fsSL "$url" -o "$work/section3" || die "download failed: $url"
curl -fsSL "$url.minisig" -o "$work/section3.minisig" ||
    die "no signature published for $url — refusing to install"

verify "$work/section3" "$work/section3.minisig" "$work"

dir=$(pick_dir)
mkdir -p "$dir" || die "could not create $dir"
[ -w "$dir" ] || die "$dir is not writable; set SECTION3_INSTALL_DIR to somewhere you own"

chmod +x "$work/section3"
mv "$work/section3" "$dir/section3" || die "could not install into $dir"

say "installed $dir/section3 (v$version)"
if ! need section3; then
    echo
    echo "$dir is not on your PATH. Add it:"
    echo "    export PATH=\"$dir:\$PATH\""
fi
echo
echo "Next: section3 self version"
