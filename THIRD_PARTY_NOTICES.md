# Runtime notices

The Blackbird image contains the statically linked Blackbird binary and the
following Go module dependencies:

- `gopkg.in/yaml.v3` — MIT license
- `github.com/gorilla/websocket` — BSD-2-Clause license
- `golang.org/x/crypto` — BSD-3-Clause license

The complete license text for each dependency is included in the upstream
module source used by the build. The rTorrent image is separately licensed
under GPL-2.0-or-later and ships its source-version notices with the image.

## Extractor (unpack on completion)

The Blackbird image bundles `p7zip` (the `7z` binary) for archive
extraction. p7zip is licensed under the GNU LGPL 2.1 with the unRAR license
restriction applying to the RAR codec: RAR decompression may not be used to
develop a RAR-compatible archiver. Native installs need `p7zip`
(Debian/Ubuntu: `p7zip-full`; Alpine: `p7zip`) or `sevenzip` (macOS Homebrew)
on PATH.

## Country data (peers tab)

The embedded `internal/geoip/dbip-country-ipv4-num.csv.gz` is derived from the
DB-IP Lite `dbip-country` dataset, Copyright DB-IP.com, licensed under the
Creative Commons Attribution 4.0 International License (CC BY 4.0).

- Project: https://db-ip.com/db/download/ip-to-country-lite
- Mirror used for regeneration: https://github.com/ip-location-db/dbip-country
- License: https://creativecommons.org/licenses/by/4.0/

Attribution: *IP Geolocation by DB-IP* — https://db-ip.com/

