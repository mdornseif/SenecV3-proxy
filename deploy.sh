#!/bin/bash
set -euo pipefail

# Target architecture — RUTX08 uses ARMv7 (armv7l).
# Override with e.g. GOARCH=mipsle ./deploy.sh ... for MIPS-based devices.
GOARCH="${GOARCH:-arm}"
GOARM="${GOARM:-7}"

if [ $# -ne 2 ]; then
    echo "Usage: $0 <router-host> <senec-ip>"
    echo "Example: $0 root@192.168.18.1 192.168.18.24"
    exit 1
fi

ROUTER_HOST="$1"
SENEC_IP="$2"
REMOTE_BINARY="/usr/local/bin/senec_proxy"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# ── Build ──────────────────────────────────────────────────────────────────────
echo "==> Building for linux/${GOARCH}..."
GOOS=linux GOARCH="${GOARCH}" GOARM="${GOARM}" go build \
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

# ── Upload ─────────────────────────────────────────────────────────────────────
echo "==> Stopping service (if running) before replacing binary..."
ssh "${ROUTER_HOST}" '/etc/init.d/senec_proxy stop 2>/dev/null; sleep 1; true'

echo "==> Uploading to ${ROUTER_HOST}..."
scp "${WORK_DIR}/senec_proxy"      "${ROUTER_HOST}:${REMOTE_BINARY}"
scp "${WORK_DIR}/senec_proxy.init" "${ROUTER_HOST}:/etc/init.d/senec_proxy"

# ── keep.d (belt-and-suspenders; /lib may be read-only on some RutOS builds) ──
if ssh "${ROUTER_HOST}" 'mkdir -p /lib/upgrade/keep.d && touch /lib/upgrade/keep.d/.test && rm /lib/upgrade/keep.d/.test' 2>/dev/null; then
    ssh "${ROUTER_HOST}" 'printf "/usr/local/bin/senec_proxy\n/etc/init.d/senec_proxy\n/lib/upgrade/keep.d/senec_proxy\n" > /lib/upgrade/keep.d/senec_proxy'
    echo "    keep.d entry written."
else
    echo "    WARNING: /lib/upgrade/keep.d is read-only on this device — skipping."
    echo "    Persistence relies on 'Keep settings' in the firmware upgrade WebUI."
fi

# ── Configure and start ────────────────────────────────────────────────────────
echo "==> Enabling service..."
ssh "${ROUTER_HOST}" \
    'chmod +x /usr/local/bin/senec_proxy /etc/init.d/senec_proxy \
     && /etc/init.d/senec_proxy enable \
     && (/etc/init.d/senec_proxy restart 2>/dev/null || /etc/init.d/senec_proxy start)'

# ── Verify ─────────────────────────────────────────────────────────────────────
# Extract the host portion of the SSH target (strip user@ prefix if present)
ROUTER_IP="${ROUTER_HOST##*@}"

echo "==> Waiting for proxy to start..."
sleep 2

echo "==> Verifying proxy responds at http://${ROUTER_IP}:8080/..."
RESPONSE=$(curl -sf --max-time 10 "http://${ROUTER_IP}:8080/" 2>&1) || {
    echo ""
    echo "ERROR: proxy did not respond. Check status with:"
    echo "  ssh ${ROUTER_HOST} '/etc/init.d/senec_proxy status'"
    echo "  ssh ${ROUTER_HOST} 'logread | grep senec'"
    exit 1
}

# Sanity-check: response should be a JSON object
if ! echo "${RESPONSE}" | grep -q '"ENERGYx'; then
    echo ""
    echo "WARNING: proxy responded but output looks unexpected:"
    echo "${RESPONSE}" | head -5
    exit 1
fi

echo ""
echo "OK — proxy is up. Sample output:"
echo "${RESPONSE}" | grep -E '"ENERGYx(GUI_HOUSE_POW|GUI_GRID_POW|GUI_BAT_DATA_POWER)"' | head -3
echo ""
echo "Done."
echo ""
echo "IMPORTANT — when upgrading firmware via the WebUI:"
echo "  Enable 'Keep settings' in System → Firmware → Update Firmware."
echo "  This preserves /usr/local/ and /etc/, combined with /lib/upgrade/keep.d/"
echo "  so the binary and init script survive the upgrade."
