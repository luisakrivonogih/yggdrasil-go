#!/bin/sh

# Convenience installer for the Garlic Routing Overlay branch of Yggdrasil.
#
# Builds an actual .deb or .rpm from source (autodetecting your package
# manager and CPU architecture), installs it, enables the Garlic Routing
# Overlay in the generated config, restarts the service, and prints a
# quick sanity check so you can confirm Garlic actually started.
#
# Usage (as root, on the server you want to test on):
#   curl -fsSL https://raw.githubusercontent.com/luisakrivonogih/yggdrasil-go/develop/install.sh | sh
# or, from an existing checkout:
#   sudo sh install.sh
#
# Environment overrides:
#   REPO_URL          git URL to build from (default: this fork, develop branch)
#   REPO_BRANCH       branch to build (default: develop)
#   WORKDIR           scratch dir for clone/build/toolchain (default: /opt/yggdrasil-installer)
#   ENABLE_GARLIC     set to 0 to skip enabling Garlic in the config (default: 1)
#   ENABLE_DASHBOARD  set to 0 to skip building/enabling the local operator
#                     dashboard (default: 1). Needs Node.js at runtime (not
#                     just to build) - if none is found, a real Node.js
#                     build is installed system-wide under /usr/local
#                     (never touches a distro nodejs package), since the
#                     systemd-managed yggdrasil execs `node` directly and
#                     must find it on PATH.
#   GARLIC_BOOTSTRAP_PEERS  comma-separated hex-encoded node keys of a few
#                     known Garlic-capable peers, written into
#                     Garlic.BootstrapPeers so this node's candidate pool
#                     starts non-empty (default: empty - a freshly-installed
#                     first node has nobody to bootstrap from yet).
#
# See docs/garlic-testing.md for how to actually exercise Garlic (build a
# circuit, send/receive a payload) once this has installed and started it -
# or, with the dashboard enabled, watch it happen at http://127.0.0.1:8080
# (loopback-only, tunnel with `ssh -L 8080:127.0.0.1:8080`).

set -e

REPO_URL=${REPO_URL:-https://github.com/luisakrivonogih/yggdrasil-go.git}
REPO_BRANCH=${REPO_BRANCH:-develop}
WORKDIR=${WORKDIR:-/opt/yggdrasil-installer}
ENABLE_GARLIC=${ENABLE_GARLIC:-1}
ENABLE_DASHBOARD=${ENABLE_DASHBOARD:-1}
GARLIC_BOOTSTRAP_PEERS=${GARLIC_BOOTSTRAP_PEERS:-}

log() { echo "==> $*"; }
die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "this needs to run as root (e.g. sudo sh install.sh) - it installs a system package and a systemd service"

# ---- 1. Detect package manager ----
if command -v apt-get >/dev/null 2>&1; then
  PKGKIND=deb
elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
  PKGKIND=rpm
else
  die "no supported package manager found (need apt-get, dnf, or yum)"
fi

# ---- 2. Detect architecture ----
case "$(uname -m)" in
  x86_64) PKGARCH=amd64; GOTARBALLARCH=amd64 ;;
  aarch64) PKGARCH=arm64; GOTARBALLARCH=arm64 ;;
  armv7l|armv6l) PKGARCH=armhf; GOTARBALLARCH=armv6l ;;
  i686|i386) PKGARCH=i386; GOTARBALLARCH=386 ;;
  *) die "unsupported architecture: $(uname -m) - build manually, see contrib/deb/generate.sh or contrib/rpm/generate.sh" ;;
esac
log "Detected $PKGKIND packaging, arch $PKGARCH"

# ---- 3. Build prerequisites ----
case "$PKGKIND" in
  deb)
    log "Installing build prerequisites (git, binutils, curl)"
    apt-get update -y
    apt-get install -y git binutils gzip ca-certificates curl
    ;;
  rpm)
    log "Installing build prerequisites (git, rpm-build, curl)"
    if command -v dnf >/dev/null 2>&1; then
      dnf install -y git rpm-build ca-certificates curl
    else
      yum install -y git rpm-build ca-certificates curl
    fi
    ;;
esac

# ---- 4. Get the source ----
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd -P) || SCRIPT_DIR=""
if [ -n "$SCRIPT_DIR" ] && grep -q '^module github.com/yggdrasil-network/yggdrasil-go$' "$SCRIPT_DIR/go.mod" 2>/dev/null; then
  SRC_DIR="$SCRIPT_DIR"
  log "Running from an existing checkout at $SRC_DIR"
else
  SRC_DIR="$WORKDIR/src"
  log "Cloning $REPO_URL ($REPO_BRANCH branch) into $SRC_DIR"
  mkdir -p "$WORKDIR"
  rm -rf "$SRC_DIR"
  git clone --branch "$REPO_BRANCH" "$REPO_URL" "$SRC_DIR"
fi
cd "$SRC_DIR"

# ---- 5. Ensure a Go toolchain new enough to bootstrap go.mod's own ----
# go.mod pins an exact Go version (see `go` directive). Go >=1.21 handles
# this itself: `go build` transparently downloads and uses the pinned
# version (GOTOOLCHAIN=auto, the default) if the local `go` is older. So
# we only need *some* Go >=1.21 present - if there's none at all, fetch a
# private bootstrap copy under $WORKDIR rather than touching any system Go.
NEED_BOOTSTRAP=1
if command -v go >/dev/null 2>&1; then
  GOVER=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
  GOMAJOR=$(echo "$GOVER" | cut -d. -f1)
  GOMINOR=$(echo "$GOVER" | cut -d. -f2)
  case "$GOMAJOR" in ''|*[!0-9]*) GOMAJOR=0 ;; esac
  case "$GOMINOR" in ''|*[!0-9]*) GOMINOR=0 ;; esac
  if [ "$GOMAJOR" -gt 1 ] || { [ "$GOMAJOR" -eq 1 ] && [ "$GOMINOR" -ge 21 ]; }; then
    NEED_BOOTSTRAP=0
    log "Found system Go $GOVER (>=1.21, can self-upgrade to what go.mod needs)"
  fi
fi

if [ "$NEED_BOOTSTRAP" = "1" ]; then
  GO_BOOTSTRAP_DIR="$WORKDIR/go-bootstrap"
  if [ ! -x "$GO_BOOTSTRAP_DIR/go/bin/go" ]; then
    log "No suitable Go toolchain found - downloading a bootstrap Go into $GO_BOOTSTRAP_DIR"
    GOVERSION=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -n1)
    [ -n "$GOVERSION" ] || die "could not determine the latest Go version (network issue?)"
    mkdir -p "$GO_BOOTSTRAP_DIR"
    curl -fsSL "https://go.dev/dl/${GOVERSION}.linux-${GOTARBALLARCH}.tar.gz" -o "$GO_BOOTSTRAP_DIR/go.tar.gz"
    tar -C "$GO_BOOTSTRAP_DIR" -xzf "$GO_BOOTSTRAP_DIR/go.tar.gz"
    rm -f "$GO_BOOTSTRAP_DIR/go.tar.gz"
  fi
  PATH="$GO_BOOTSTRAP_DIR/go/bin:$PATH"
  export PATH
  log "Using bootstrap $(go version)"
fi
export GOTOOLCHAIN=auto

# ---- 6. Ensure Node.js is available (dashboard only) ----
# Unlike the Go toolchain (only needed here, to build), the dashboard
# needs `node` present on PATH at *runtime* too - the systemd-managed
# yggdrasil execs it directly (see src/dashboard/dashboard.go). A
# private, only-visible-to-this-script copy (like the Go bootstrap
# above) would not be found by the service, so a suitable Node.js is
# installed system-wide under /usr/local instead - a real upstream
# Node.js release, never a distro nodejs package (whose version varies
# wildly and is often too old for this project's SvelteKit/Vite
# toolchain).
if [ "$ENABLE_DASHBOARD" = "1" ]; then
  NEED_NODE=1
  if command -v node >/dev/null 2>&1; then
    NODEVER=$(node -v | sed 's/^v//')
    NODEMAJOR=$(echo "$NODEVER" | cut -d. -f1)
    case "$NODEMAJOR" in ''|*[!0-9]*) NODEMAJOR=0 ;; esac
    if [ "$NODEMAJOR" -ge 20 ]; then
      NEED_NODE=0
      log "Found system Node.js v$NODEVER (>=20, good enough)"
    fi
  fi
  case "$GOTARBALLARCH" in
    amd64) NODEARCH=x64 ;;
    arm64) NODEARCH=arm64 ;;
    armv6l) NODEARCH=armv7l ;;
    *) NODEARCH="" ;;
  esac
  if [ "$NEED_NODE" = "1" ] && [ -z "$NODEARCH" ]; then
    log "No official Node.js build for this architecture - skipping the dashboard (ENABLE_DASHBOARD=0 to silence this)"
    ENABLE_DASHBOARD=0
  elif [ "$NEED_NODE" = "1" ]; then
    log "No suitable system Node.js (need >=20) - installing one system-wide under /usr/local/lib/nodejs-yggdashboard"
    NODE_INDEX_URL="https://nodejs.org/dist/index.json"
    NODEVERSION=""
    if command -v jq >/dev/null 2>&1; then
      NODEVERSION=$(curl -fsSL "$NODE_INDEX_URL" | jq -r '[.[] | select(.lts != false)][0].version')
    elif command -v python3 >/dev/null 2>&1; then
      NODEVERSION=$(curl -fsSL "$NODE_INDEX_URL" | python3 -c "import json,sys; print(next(d['version'] for d in json.load(sys.stdin) if d['lts']))")
    fi
    [ -n "$NODEVERSION" ] || die "could not determine the latest Node.js LTS version (network issue, or neither jq nor python3 is installed)"
    NODE_INSTALL_DIR=/usr/local/lib/nodejs-yggdashboard
    rm -rf "$NODE_INSTALL_DIR"
    mkdir -p "$NODE_INSTALL_DIR"
    curl -fsSL "https://nodejs.org/dist/${NODEVERSION}/node-${NODEVERSION}-linux-${NODEARCH}.tar.xz" -o "$WORKDIR/node.tar.xz"
    tar -C "$NODE_INSTALL_DIR" --strip-components=1 -xJf "$WORKDIR/node.tar.xz"
    rm -f "$WORKDIR/node.tar.xz"
    for bin in node npm npx; do
      ln -sf "$NODE_INSTALL_DIR/bin/$bin" "/usr/local/bin/$bin"
    done
    PATH="/usr/local/bin:$PATH"
    export PATH
    log "Installed Node.js $NODEVERSION, symlinked into /usr/local/bin (on the systemd service's default PATH)"
  fi
fi

# ---- 7. Build and package ----
log "Building and packaging ($PKGKIND, $PKGARCH) - first run also fetches the go.mod-pinned Go toolchain, can take a few minutes"
rm -f ./*.deb ./*.rpm 2>/dev/null || true
case "$PKGKIND" in
  deb) PKGARCH=$PKGARCH sh contrib/deb/generate.sh ;;
  rpm) PKGARCH=$PKGARCH sh contrib/rpm/generate.sh ;;
esac
PKGFILE=$(ls -t ./*."$PKGKIND" 2>/dev/null | head -n1)
[ -n "$PKGFILE" ] && [ -f "$PKGFILE" ] || die "package build did not produce a .$PKGKIND file"
log "Built $PKGFILE"

# ---- 8. Install ----
log "Installing $PKGFILE"
case "$PKGKIND" in
  deb)
    dpkg -i "$PKGFILE" || apt-get install -y -f
    ;;
  rpm)
    if command -v dnf >/dev/null 2>&1; then
      dnf install -y "./$PKGFILE"
    else
      rpm -Uvh "$PKGFILE"
    fi
    ;;
esac

# The package's postinstall step already generated /etc/yggdrasil/yggdrasil.conf
# (Garlic disabled, the project default) and started the service - see
# contrib/deb/generate.sh's postinst / contrib/rpm/generate.sh's %post.

# ---- 9. Build and install the dashboard ----
if [ "$ENABLE_DASHBOARD" = "1" ]; then
  log "Building the dashboard (npm install && npm run build) - first run can take a minute"
  ( cd "$SRC_DIR/yggdashboard" && npm install --no-audit --no-fund && npm run build )
  DASHBOARD_INSTALL_DIR=/usr/lib/yggdrasil/dashboard
  rm -rf "$DASHBOARD_INSTALL_DIR"
  mkdir -p "$DASHBOARD_INSTALL_DIR"
  cp -r "$SRC_DIR/yggdashboard/build/." "$DASHBOARD_INSTALL_DIR/"
  chown -R root:yggdrasil "$DASHBOARD_INSTALL_DIR"
  chmod -R go-w "$DASHBOARD_INSTALL_DIR"
  log "Dashboard built assets installed to $DASHBOARD_INSTALL_DIR (yggdrasil's default search path, no Dashboard.Path config needed)"
fi

# ---- 10. Enable Garlic / the dashboard ----
if [ "$ENABLE_GARLIC" = "1" ] || [ "$ENABLE_DASHBOARD" = "1" ] || [ -n "$GARLIC_BOOTSTRAP_PEERS" ]; then
  log "Updating /etc/yggdrasil/yggdrasil.conf (Garlic=$ENABLE_GARLIC, Dashboard=$ENABLE_DASHBOARD)"
  TMP_JSON="$WORKDIR/yggdrasil.json"
  mkdir -p "$WORKDIR"
  yggdrasil -useconffile /etc/yggdrasil/yggdrasil.conf -normaliseconf -json > "$TMP_JSON"

  EDITED=0
  if command -v jq >/dev/null 2>&1; then
    jq --argjson garlic "$([ "$ENABLE_GARLIC" = "1" ] && echo true || echo false)" \
       --argjson dash "$([ "$ENABLE_DASHBOARD" = "1" ] && echo true || echo false)" \
       --arg bootstrap "$GARLIC_BOOTSTRAP_PEERS" \
       '.Garlic.Enabled = (if $garlic then true else .Garlic.Enabled end)
        | .Dashboard.Enabled = (if $dash then true else .Dashboard.Enabled end)
        | .Garlic.BootstrapPeers = (if $bootstrap != "" then ($bootstrap | split(",")) else .Garlic.BootstrapPeers end)' \
       "$TMP_JSON" > "$TMP_JSON.new" && EDITED=1
  elif command -v python3 >/dev/null 2>&1; then
    ENABLE_GARLIC="$ENABLE_GARLIC" ENABLE_DASHBOARD="$ENABLE_DASHBOARD" GARLIC_BOOTSTRAP_PEERS="$GARLIC_BOOTSTRAP_PEERS" python3 - "$TMP_JSON" > "$TMP_JSON.new" <<'PY' && EDITED=1
import json, os, sys
with open(sys.argv[1]) as f:
    cfg = json.load(f)
if os.environ.get("ENABLE_GARLIC") == "1":
    cfg.setdefault("Garlic", {})["Enabled"] = True
if os.environ.get("ENABLE_DASHBOARD") == "1":
    cfg.setdefault("Dashboard", {})["Enabled"] = True
GARLIC_BOOTSTRAP_PEERS = os.environ.get("GARLIC_BOOTSTRAP_PEERS", "")
if GARLIC_BOOTSTRAP_PEERS:
    cfg.setdefault("Garlic", {})["BootstrapPeers"] = GARLIC_BOOTSTRAP_PEERS.split(",")
json.dump(cfg, sys.stdout, indent=2)
PY
  fi

  if [ "$EDITED" = "1" ] && [ -s "$TMP_JSON.new" ]; then
    yggdrasil -useconffile "$TMP_JSON.new" -normaliseconf > /etc/yggdrasil/yggdrasil.conf
    chown root:yggdrasil /etc/yggdrasil/yggdrasil.conf
    chmod 640 /etc/yggdrasil/yggdrasil.conf
    rm -f "$TMP_JSON" "$TMP_JSON.new"
    systemctl restart yggdrasil
    log "Config updated, yggdrasil restarted"
  else
    log "Neither jq nor python3 found - enable manually: set \"Garlic\": { \"Enabled\": true } and/or \"Dashboard\": { \"Enabled\": true } (and, if bootstrapping, \"Garlic\": { \"BootstrapPeers\": [...] }) in /etc/yggdrasil/yggdrasil.conf, then run 'systemctl restart yggdrasil'"
  fi
fi

# ---- 11. Verify ----
sleep 2
log "Verifying"
if systemctl is-active --quiet yggdrasil; then
  log "yggdrasil.service is active"
else
  log "yggdrasil.service is NOT active - check: journalctl -u yggdrasil -n 50 --no-pager"
fi

echo
echo "--- Yggdrasil node ---"
yggdrasilctl getself || true
echo
echo "--- Garlic identity (present only if Garlic started successfully) ---"
yggdrasilctl getGarlicIdentity || true
echo
echo "--- Garlic stats ---"
yggdrasilctl getGarlicStats || true
if [ "$ENABLE_DASHBOARD" = "1" ]; then
  echo
  echo "--- Dashboard ---"
  if journalctl -u yggdrasil -n 30 --no-pager 2>/dev/null | grep -q "Dashboard started"; then
    echo "started - loopback-only at http://127.0.0.1:8080"
    echo "view it remotely with: ssh -L 8080:127.0.0.1:8080 <this-server>"
  else
    echo "did not report starting - check: journalctl -u yggdrasil -n 50 --no-pager | grep -i dashboard"
  fi
fi
echo
echo "Done. To actually exercise Garlic (build a circuit through a peer, send/receive"
echo "a payload, try the newer padding/jitter/discovery/multipath/bundling defenses),"
echo "see $SRC_DIR/docs/garlic-testing.md - you'll need at least one more Garlic-enabled"
echo "peer (run this installer there too, or peer with an existing Yggdrasil node - only"
echo "the nodes you explicitly build a circuit through need Garlic enabled). The dashboard"
echo "(if enabled) shows those circuits live under /circuits and /garlic once you peer."
