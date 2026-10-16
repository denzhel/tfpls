#!/usr/bin/env bash
# Tests for actions/setup/install.sh against a local HTTP server that can fail on demand. No real network.
# Run: bash tests/install.test.sh   (needs python3, curl, unzip, tar)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
INSTALL="$here/actions/setup/install.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'kill "${SRV:-}" 2>/dev/null; rm -rf "$T"' EXIT

# ---- fixtures: fake release assets laid out like the GitHub release URLs, with real checksum files
python3 - "$T/srv" <<'PY'
import hashlib, io, os, sys, tarfile, zipfile
root = sys.argv[1]
def sha(b): return hashlib.sha256(b).hexdigest()
tofu = b"#!/bin/sh\necho 'OpenTofu v9.9.9 (fake)'\n"
conf = b"#!/bin/sh\necho 'Conftest: 9.9.9 (fake)'\n"
tdir = os.path.join(root, "v9.9.9"); os.makedirs(tdir)
for os_, arch in (("linux","amd64"),("linux","arm64"),("darwin","amd64"),("darwin","arm64")):
    name = f"tofu_9.9.9_{os_}_{arch}.zip"; buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as z:
        zi = zipfile.ZipInfo("tofu"); zi.external_attr = 0o755 << 16; z.writestr(zi, tofu)
    open(os.path.join(tdir, name), "wb").write(buf.getvalue())
sums = "".join(f"{sha(open(os.path.join(tdir,n),'rb').read())}  {n}\n" for n in sorted(os.listdir(tdir)))
open(os.path.join(tdir, "tofu_9.9.9_SHA256SUMS"), "w").write(sums)
cdir = os.path.join(root, "c", "v9.9.9"); os.makedirs(cdir)
lines = ""
for os_, arch in (("Linux","x86_64"),("Linux","arm64"),("Darwin","x86_64"),("Darwin","arm64")):
    name = f"conftest_9.9.9_{os_}_{arch}.tar.gz"; buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as t:
        ti = tarfile.TarInfo("conftest"); ti.size = len(conf); ti.mode = 0o755; t.addfile(ti, io.BytesIO(conf))
    b = buf.getvalue(); open(os.path.join(cdir, name), "wb").write(b); lines += f"{sha(b)}  {name}\n"
open(os.path.join(cdir, "checksums.txt"), "w").write(lines)
PY

# ---- server: serves $T/srv; the first $FAIL requests get HTTP 500; every request is logged
cat > "$T/server.py" <<'PY'
import http.server, os, sys, threading
root, failn, log, portfile = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
lock = threading.Lock(); state = {"left": failn}
class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k): super().__init__(*a, directory=root, **k)
    def log_message(self, *a): pass
    def do_GET(self):
        with lock:
            open(log, "a").write(self.path + "\n")
            if state["left"] > 0:
                state["left"] -= 1; self.send_response(500); self.end_headers(); return
        super().do_GET()
s = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
open(portfile, "w").write(str(s.server_address[1])); s.serve_forever()
PY

start_server() { # $1 = number of initial failures
  rm -f "$T/req.log" "$T/port"; : > "$T/req.log"
  python3 "$T/server.py" "$T/srv" "$1" "$T/req.log" "$T/port" & SRV=$!
  for _ in $(seq 1 50); do [ -s "$T/port" ] && break; sleep 0.1; done
  PORT=$(cat "$T/port")
}
stop_server() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }

# run_install <version dir name> [env ...] -> rc, OUT
run_install() {
  OUT=$(env -i PATH="$PATH" HOME="$T/home" GITHUB_PATH="$T/ghpath" TFPLS_TOOL_CACHE="$T/cache" TFPLS_RETRY_DELAY=0 \
      TFPLS_OS=linux TFPLS_ARCH=amd64 TOFU_VERSION=9.9.9 CONFTEST_VERSION=9.9.9 \
      TFPLS_TOFU_BASE_URL="http://127.0.0.1:$PORT" TFPLS_CONFTEST_BASE_URL="http://127.0.0.1:$PORT/c" "$@" bash "$INSTALL" 2>&1); rc=$?
}
fresh() { rm -rf "$T/cache" "$T/ghpath"; : > "$T/ghpath"; }

# 1) clean install
fresh; start_server 0; run_install
[ $rc -eq 0 ] && ok "clean install succeeds" || bad "clean install (rc=$rc): $OUT"
[ -x "$T/cache/opentofu/9.9.9/linux-amd64/tofu" ] && [ -x "$T/cache/conftest/9.9.9/linux-amd64/conftest" ] && ok "both binaries are installed and executable" || bad "binaries missing"
[ "$("$T/cache/opentofu/9.9.9/linux-amd64/tofu")" = "OpenTofu v9.9.9 (fake)" ] && ok "installed tofu is the one from the archive" || bad "tofu content"
grep -q "opentofu/9.9.9/linux-amd64" "$T/ghpath" && grep -q "conftest/9.9.9/linux-amd64" "$T/ghpath" && ok "directories are added to GITHUB_PATH" || bad "GITHUB_PATH"
case "$OUT" in *"checksum verified"*) ok "checksum verification is reported";; *) bad "no checksum message: $OUT";; esac

# 2) second run: cache hit, no network
before=$(wc -l < "$T/req.log"); run_install
[ $rc -eq 0 ] && [ "$(wc -l < "$T/req.log")" = "$before" ] && ok "cache hit makes no requests" || bad "cache hit (rc=$rc, requests $before -> $(wc -l < "$T/req.log"))"
# no policy configured: conftest is not downloaded
fresh; : > "$T/req.log"; run_install INSTALL_CONFTEST=false
[ $rc -eq 0 ] && [ -x "$T/cache/opentofu/9.9.9/linux-amd64/tofu" ] && [ ! -e "$T/cache/conftest" ] && ! grep -q '/c/' "$T/req.log" && ok "INSTALL_CONFTEST=false installs only tofu and makes no conftest request" || bad "skip conftest (rc=$rc): $OUT"
stop_server

# 3) transient failures: first 3 requests return 500, install still succeeds
fresh; start_server 3; run_install
[ $rc -eq 0 ] && ok "survives 3 transient server errors" || bad "transient errors (rc=$rc): $OUT"
[ -x "$T/cache/opentofu/9.9.9/linux-amd64/tofu" ] && ok "binary present after retries" || bad "binary after retries"
stop_server

# 4) permanent failure: clear error, nothing installed
fresh; start_server 100000; run_install
[ $rc -ne 0 ] && ok "permanent failure ends in an error" || bad "permanent failure should fail"
case "$OUT" in *"download failed after"*) ok "error names the failed download";; *) bad "error text: $OUT";; esac
[ ! -e "$T/cache/opentofu" ] && ok "nothing is installed after a failed download" || bad "partial install left behind"
stop_server

# 5) corrupted archive: checksum mismatch refuses to install
fresh; cp "$T/srv/v9.9.9/tofu_9.9.9_linux_amd64.zip" "$T/good.zip"; echo "tampered" >> "$T/srv/v9.9.9/tofu_9.9.9_linux_amd64.zip"
start_server 0; run_install
[ $rc -ne 0 ] && ok "tampered archive is refused" || bad "tampered archive accepted"
case "$OUT" in *"checksum mismatch"*) ok "checksum mismatch is reported";; *) bad "mismatch text: $OUT";; esac
[ ! -e "$T/cache/opentofu" ] && ok "tampered archive is not installed" || bad "tampered install left behind"
stop_server; cp "$T/good.zip" "$T/srv/v9.9.9/tofu_9.9.9_linux_amd64.zip"

# 6) checksum file without the archive's entry
fresh; grep -v "linux_amd64" "$T/srv/v9.9.9/tofu_9.9.9_SHA256SUMS" > "$T/sums.tmp"; cp "$T/srv/v9.9.9/tofu_9.9.9_SHA256SUMS" "$T/sums.orig"; cp "$T/sums.tmp" "$T/srv/v9.9.9/tofu_9.9.9_SHA256SUMS"
start_server 0; run_install
[ $rc -ne 0 ] && ok "missing checksum entry is refused" || bad "missing checksum accepted"
stop_server; cp "$T/sums.orig" "$T/srv/v9.9.9/tofu_9.9.9_SHA256SUMS"

# 7) other platforms resolve to the right asset names
for combo in "darwin arm64" "linux arm64" "darwin amd64"; do
  set -- $combo; fresh; start_server 0; run_install TFPLS_OS="$1" TFPLS_ARCH="$2"
  [ $rc -eq 0 ] && [ -x "$T/cache/opentofu/9.9.9/$1-$2/tofu" ] && ok "installs for $1/$2" || bad "install for $1/$2 (rc=$rc): $OUT"
  stop_server
done
fresh; start_server 0; run_install TFPLS_OS=freebsd; [ $rc -ne 0 ] && ok "unsupported OS is rejected" || bad "unsupported OS accepted"; stop_server

done_tests
