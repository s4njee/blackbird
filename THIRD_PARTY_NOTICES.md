# Runtime notices

The Blackbird image contains the statically linked Blackbird binary and the
following Go module dependencies:

- `gopkg.in/yaml.v3` — MIT license
- `github.com/gorilla/websocket` — BSD-2-Clause license
- `golang.org/x/crypto` — BSD-3-Clause license

The complete license text for each dependency is included in the upstream
module source used by the build. The rTorrent image is separately licensed
under GPL-2.0-or-later and ships its source-version notices with the image.
