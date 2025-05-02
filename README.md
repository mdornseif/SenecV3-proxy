# SenecV3-proxy

This are experiments to add a proxy to make accessing Senec devices to run on minimal hardware.
Candidates are an odroid m1s or an Teltonika RUTX08.
It allowes embedded Devices, eg a Loxone Miniserver to access Data on Senec. 

## RUTX08

To build on MacOS install the toolchain:

```sh
GOOS=linux GOARCH=arm go build -ldflags="-s -w" -o ./senec_proxy-linux-arm ./senec_proxy.go
upx --brute ./senec_proxy
```

an init script in `/etc/init.d/senec_proxy` might look loke this assuming `192.168.18.24` is the IP of your Senec device:

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

To use call `/etc/init.d/senec_proxy enable && /etc/init.d/senec_proxy start`

There is also an experimental Rust version of the proxy.