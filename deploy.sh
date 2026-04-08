#!/bin/bash
set -euo pipefail

# Target architecture — RUTX08 uses MIPS (mipsle); adjust if your device differs.
# Override with: GOARCH=arm ./deploy.sh ...
GOARCH="${GOARCH:-mipsle}"

usage() {
    cat << 'USAGE'
Usage: ./deploy.sh <router-host> <senec-ip>

  router-host   SSH target for the RutOS device, e.g. root@192.168.1.1
  senec-ip      IP address of the SENEC device,   e.g. 192.168.18.24

Environment variables:
  GOARCH    Go target architecture (default: mipsle for RUTX08)

Example:
  ./deploy.sh root@192.168.1.1 192.168.18.24
USAGE
    exit 1
}

[ $# -eq 2 ] || usage

ROUTER_HOST="$1"
SENEC_IP="$2"
REMOTE_BINARY="/usr/local/bin/senec_proxy"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# ── Build ──────────────────────────────────────────────────────────────────────
echo "==> Building for linux/${GOARCH}..."
GOOS=linux GOARCH="${GOARCH}" go build \
    -ldflags="-s -w" \
    -o "${WORK_DIR}/senec_proxy" \
    ./senec_proxy.go

if command -v upx &>/dev/null; then
    echo "==> Compressing with upx (--brute is slow but thorough)..."
    upx --brute "${WORK_DIR}/senec_proxy"
else
    echo "    upx not found — skipping compression (install upx to reduce binary size)"
fi

# ── Generate init script ───────────────────────────────────────────────────────
cat > "${WORK_DIR}/senec_proxy.init" << EOF
#!/bin/sh /etc/rc.common

START=90
STOP=01
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command ${REMOTE_BINARY} ${SENEC_IP} 0.0.0.0
    procd_set_param user nobody
    procd_set_param stdout 0
    procd_set_param stderr 0
    procd_set_param pidfile /var/run/senec_proxy.pid
    procd_close_instance
}
EOF

# ── Generate keep.d file (survives firmware upgrades) ─────────────────────────
cat > "${WORK_DIR}/senec_proxy.keepd" << 'EOF'
/usr/local/bin/senec_proxy
/etc/init.d/senec_proxy
/lib/upgrade/keep.d/senec_proxy
EOF

# ── Upload ─────────────────────────────────────────────────────────────────────
echo "==> Uploading to ${ROUTER_HOST}..."
scp "${WORK_DIR}/senec_proxy"       "${ROUTER_HOST}:${REMOTE_BINARY}"
scp "${WORK_DIR}/senec_proxy.init"  "${ROUTER_HOST}:/etc/init.d/senec_proxy"
ssh "${ROUTER_HOST}" 'mkdir -p /lib/upgrade/keep.d'
scp "${WORK_DIR}/senec_proxy.keepd" "${ROUTER_HOST}:/lib/upgrade/keep.d/senec_proxy"

# ── Configure and start ────────────────────────────────────────────────────────
echo "==> Enabling service..."
ssh "${ROUTER_HOST}" \
    'chmod +x /usr/local/bin/senec_proxy /etc/init.d/senec_proxy \
     && /etc/init.d/senec_proxy enable \
     && (/etc/init.d/senec_proxy restart 2>/dev/null || /etc/init.d/senec_proxy start)'

echo ""
echo "Done. Proxy is running on ${ROUTER_HOST}."
echo "Verify: ssh ${ROUTER_HOST} 'curl -s http://localhost:8080/ | head -5'"
echo ""
echo "IMPORTANT — when upgrading firmware via the WebUI:"
echo "  Enable 'Keep settings' in System → Firmware → Update Firmware."
echo "  This preserves /usr/local/ and /etc/, combined with /lib/upgrade/keep.d/"
echo "  the binary and init script will survive the upgrade."
