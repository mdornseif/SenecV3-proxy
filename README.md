# SenecV3-proxy

This are experiments to add a proxy to make accessing Senec devices to run on minimal hardware.
Candidates are an odroid m1s or an Teltonika RUTX08.

## RUTX08

To build on MacOS install the toolchain:

```sh
$ rustup target add armv7-unknown-linux-gnueabi
$ cargo build --target armv7-unknown-linux-gnueabi --release
```


