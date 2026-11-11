# Test data

Inform packets captured from real UniFi access points, from
[dmke/inform-inspect](https://github.com/dmke/inform-inspect) (via the
jacobalberty/inform-inspect fork), used under the MIT License:

    Copyright (c) 2018 Dominik Menke

| File             | Device                                      | Encoding           | Device key                         |
|------------------|---------------------------------------------|--------------------|------------------------------------|
| `aes_snappy.dat` | UAP-AC-Pro-Gen2 f0:9f:c2:79:63:90, fw 3.9.27.8537 | AES-CBC + snappy | `e2c930683af3945e4d0d58d37a78c2a6` |
| `gcm_zlib.dat`   | UAP-AC-Pro-Gen2 fc:ec:da:fc:c5:35, fw 4.0.66.10832 | AES-GCM + zlib  | `05223125e6b44aca0a0599551168d766` |

Both are device-to-controller informs; no controller reply has been
captured, so replies are only verified by round trip.

`system-cfg-keys.txt` lists the key names of a system.cfg captured from a
dual-radio access point (madwifi driver) managed by a UniFi controller, with
a WPA2/3-enterprise SSID using RADIUS authentication and accounting, a
WPA-PSK SSID and a guest SSID on VLAN 5. Numeric indexes are replaced by
`N`, and the values (which include the device key, passphrases and RADIUS
secrets) are not kept. The renderer's tests check every key it writes
against this list.
