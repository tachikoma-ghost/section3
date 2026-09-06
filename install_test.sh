#!/bin/sh
# Differential test of install.sh against the REAL published release.
#
#   sh install_test.sh
#
# Needs network, python3 (a throwaway HTTP server for the tamper cases) and
# minisign. Not part of `make test` for that reason.
#
# It runs entirely inside a temp directory and never near an installed
# section3: every case sets SECTION3_INSTALL_DIR.
#
# Two lessons are baked in, both learned by this file lying:
#
#  - Every case asserts WHY it passed, not just the exit code. A refusal that
#    happened because a download 404'd looks identical from the exit status to
#    one that happened because a signature was bad.
#  - The local server binds a free port and serves a nonce the test then
#    demands to see. A stale server from an earlier run once held the fixed
#    port and served good bytes, so the tamper cases "passed" against content
#    nobody had tampered with.
set -u

SCRIPT=${SCRIPT:-$(cd "$(dirname "$0")" && pwd)/install.sh}
BASE=${BASE:-https://signalshell.com/releases/section3}
REF_VERSION=${REF_VERSION:-3}   # a published version to pull real artifacts from

pass=0; fail=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); }
# check <desc> <got> <want> <log> <required substring>
check() {
    if [ "$2" -eq "$3" ] && grep -q "$5" "$4"; then ok "$1"
    else bad "$1 (exit=$2 want=$3; no '$5' in $4)"; sed 's/^/        /' "$4" | tail -4; fi
}

command -v minisign >/dev/null 2>&1 || { echo "SKIP: minisign not installed"; exit 0; }
command -v python3  >/dev/null 2>&1 || { echo "SKIP: python3 not installed"; exit 0; }

WORK=$(mktemp -d) || exit 1
cleanup() { [ -n "${SRVPID:-}" ] && kill "$SRVPID" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM
cd "$WORK" || exit 1
echo "work: $WORK"

# --- a PATH with everything install.sh needs EXCEPT minisign, so the openssl
# --- branch is genuinely what runs.
mkdir nomini
for t in sh dash bash curl sed dd cmp tr mktemp uname wc mv chmod mkdir rm cat \
         dirname openssl grep printf expr basename sleep; do
    src=$(command -v "$t" 2>/dev/null) && ln -sf "$src" "nomini/$t"
done
NOMINI="$WORK/nomini"
if PATH="$NOMINI" command -v minisign >/dev/null 2>&1; then
    echo "setup broken: minisign is still reachable on the stripped PATH"; exit 1
fi
echo "setup: minisign not reachable on the stripped PATH"

# --- 1 & 2: the real published release, through each verifier ---------------
mkdir d1 d2
SECTION3_INSTALL_DIR=$WORK/d1 sh "$SCRIPT" > out1.log 2>&1
check "real v$REF_VERSION installs (minisign)" $? 0 out1.log "signature verified"
[ -x d1/section3 ] && echo "        $(./d1/section3 self version 2>&1 | head -1)"

SECTION3_INSTALL_DIR=$WORK/d2 PATH="$NOMINI" "$NOMINI/sh" "$SCRIPT" > out2.log 2>&1
check "real v$REF_VERSION installs (openssl)" $? 0 out2.log "verifying signature (openssl)"
if cmp -s d1/section3 d2/section3; then echo "        both verifiers accepted the same bytes"
else bad "the two verifiers installed different bytes"; fi

# --- local server holding a tampered copy -----------------------------------
mkdir -p srv/9
curl -fsSL "$BASE/$REF_VERSION/section3-linux-amd64" -o srv/9/section3-linux-amd64 ||
    { echo "setup: could not download the reference binary"; exit 1; }
curl -fsSL "$BASE/$REF_VERSION/section3-linux-amd64.minisig" -o srv/9/section3-linux-amd64.minisig ||
    { echo "setup: could not download the reference signature"; exit 1; }
cp srv/9/section3-linux-amd64 good.bin
printf 'X' | dd of=srv/9/section3-linux-amd64 bs=1 seek=1024 conv=notrunc 2>/dev/null
cmp -s good.bin srv/9/section3-linux-amd64 &&
    { echo "setup broken: the tamper did not change the file"; exit 1; }
echo '{"version":9,"published":"test"}' > srv/latest.json

NONCE=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
echo "$NONCE" > srv/nonce.txt

# A free port, not a fixed one: a leftover server on a fixed port silently
# answers instead, and then the tamper cases test nothing.
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
( cd srv && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >/dev/null 2>&1 ) &
SRVPID=$!
i=0
while [ $i -lt 40 ]; do
    got=$(curl -fsS "http://127.0.0.1:$PORT/nonce.txt" 2>/dev/null) && [ "$got" = "$NONCE" ] && break
    i=$((i+1)); sleep 0.25
done
if [ "${got:-}" != "$NONCE" ]; then
    echo "setup broken: the server answering port $PORT is not ours"; exit 1
fi
echo "setup: our tamper server confirmed on port $PORT (nonce matched)"

sed "s|^BASE_URL=.*|BASE_URL=\"http://127.0.0.1:$PORT\"|" "$SCRIPT" > tampered.sh

# --- 3: a tampered binary must be refused by both verifiers -----------------
mkdir d3 d3o
SECTION3_INSTALL_DIR=$WORK/d3 sh tampered.sh > out3m.log 2>&1
check "tampered binary refused (minisign)" $? 1 out3m.log "SIGNATURE VERIFICATION FAILED"
[ -e d3/section3 ] && bad "the tampered binary was installed anyway (minisign)"

SECTION3_INSTALL_DIR=$WORK/d3o PATH="$NOMINI" "$NOMINI/sh" tampered.sh > out3o.log 2>&1
check "tampered binary refused (openssl)" $? 1 out3o.log "SIGNATURE VERIFICATION FAILED"
[ -e d3o/section3 ] && bad "the tampered binary was installed anyway (openssl)"

# --- 3b: positive control on the SAME server --------------------------------
# Without this, the two refusals above could both be explained by the local
# server being broken rather than by the signature being wrong.
cp good.bin srv/9/section3-linux-amd64
mkdir d3c
SECTION3_INSTALL_DIR=$WORK/d3c sh tampered.sh > out3c.log 2>&1
check "untampered bytes from the same server install" $? 0 out3c.log "signature verified"

# --- 4: nothing to verify with must be fatal --------------------------------
mkdir noverify d4
for t in sh dash curl sed tr mktemp uname wc mv chmod mkdir rm cat dirname grep expr basename; do
    src=$(command -v "$t" 2>/dev/null) && ln -sf "$src" "noverify/$t"
done
SECTION3_INSTALL_DIR=$WORK/d4 PATH="$WORK/noverify" "$WORK/noverify/sh" "$SCRIPT" > out4.log 2>&1
check "no verifier available -> refuses" $? 1 out4.log "does not install unverified"
[ -e d4/section3 ] && bad "installed with no verifier available"

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
