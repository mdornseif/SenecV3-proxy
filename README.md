# SenecV3-proxy

A proxy that makes accessing Senec home battery devices easy from minimal embedded hardware (e.g. a Teltonika RUTX08 router or an Odroid M1S).

Two endpoints are available:

| Endpoint | Description |
|---|---|
| `GET http://router:8080/` | All values as JSON |
| `GET http://router:8080/metrics` | Prometheus-compatible gauge metrics |

Instead of posting raw requests to `POST https://<IP-SENEC>/lala.cgi`, clients can simply `GET http://router:8080/` and receive clean JSON:

```json
{
  "ENERGYxGUI_BAT_DATA_POWER": -418.70001220703125,
  "ENERGYxGUI_BAT_DATA_POWERkW": -0.41870001220703124,
  "ENERGYxGUI_GRID_POW": -15.299999237060547,
  "ENERGYxGUI_GRID_POWkW": -0.015299999237060546,
  "ENERGYxGUI_HOUSE_POW": 403.4000244140625,
  "ENERGYxGUI_HOUSE_POWkW": 0.4034000244140625,
  "ENERGYxGUI_INVERTER_POWER": -0,
  "ENERGYxGUI_INVERTER_POWERkW": -0
}
```

`/metrics` exposes the same data as Prometheus gauges (SENEC namespace separators are mapped to `_`):

```
# TYPE senec_energy_gui_bat_data_power gauge
senec_energy_gui_bat_data_power 391.56
# TYPE senec_energy_gui_bat_data_powerkw gauge
senec_energy_gui_bat_data_powerkw 0.39156
# TYPE senec_energy_gui_grid_pow gauge
senec_energy_gui_grid_pow 2959.47
# TYPE senec_energy_gui_house_pow gauge
senec_energy_gui_house_pow 2970.36
```

## Deploying to a Teltonika RUTX08 (RutOS)

### One-step deploy

`deploy.sh` builds the binary, uploads it over SSH, installs the init script, and configures firmware-upgrade persistence automatically:

```sh
./deploy.sh root@192.168.18.1 192.168.18.24
#            └─ router SSH    └─ SENEC device IP
```

The script defaults to `GOARCH=arm GOARM=7` (ARMv7 in the RUTX08). Override with the `GOARCH` environment variable if your device uses a different architecture:

```sh
GOARCH=mipsle ./deploy.sh root@192.168.18.1 192.168.18.24
```

`upx` is used to compress the binary if it is installed — recommended but not required.

The script ends by curling `http://localhost:8080/` on the device and checking the response contains expected SENEC JSON keys. It exits with an error and prints diagnostic commands if the proxy doesn't respond within 10 seconds.

To verify manually after deployment:

```sh
curl -s http://192.168.18.1:8080/ | python3 -m json.tool | head -20
```

To check service status or logs:

```sh
ssh root@192.168.18.1 '/etc/init.d/senec_proxy status'
ssh root@192.168.18.1 'logread | grep senec'
```

### Surviving firmware upgrades

RutOS wipes most of the filesystem on upgrade. The deploy script uses two mechanisms together to keep the proxy running after a firmware update:

1. **`/lib/upgrade/keep.d/senec_proxy`** — lists the binary and init script so the sysupgrade tool preserves them. More reliable than `/etc/sysupgrade.conf`, which has known bugs in RutOS ≥ 07.00.

2. **"Keep settings" in the WebUI** — when upgrading via *System → Firmware → Update Firmware*, enable the **Keep settings** checkbox. This preserves `/etc/` and `/usr/local/` and is required for the `keep.d` mechanism to be effective.

> **Do not** rely solely on `/etc/sysupgrade.conf` — it is broken in RutOS firmware 07.00 and later.

After a firmware upgrade, re-enable the service with:

```sh
ssh root@192.168.1.1 '/etc/init.d/senec_proxy enable && /etc/init.d/senec_proxy start'
```

Or simply re-run `deploy.sh`, which is idempotent.

### Manual build (macOS)

```sh
# RUTX08 / Odroid M1S (ARMv7)
GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o ./senec_proxy-linux-arm ./senec_proxy.go
upx --brute ./senec_proxy-linux-arm

# MIPS devices
GOOS=linux GOARCH=mipsle go build -ldflags="-s -w" -o ./senec_proxy-linux-mipsle ./senec_proxy.go
upx --brute ./senec_proxy-linux-mipsle
```

### Manual init script

`/etc/init.d/senec_proxy`:

```sh
#!/bin/sh /etc/rc.common

START=90
STOP=01
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command /usr/local/bin/senec_proxy 192.168.18.24 0.0.0.0
    procd_set_param user nobody
    procd_set_param stdout 0
    procd_set_param stderr 0
    procd_set_param pidfile /var/run/senec_proxy.pid
    procd_close_instance
}
```

Enable and start:

```sh
/etc/init.d/senec_proxy enable && /etc/init.d/senec_proxy start
```

There is also an experimental Rust version of the proxy (`src/main.rs`).
