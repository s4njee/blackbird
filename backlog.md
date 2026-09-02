# Blackbird — Shipping and Packaging Backlog

This backlog starts from the application implemented through Epic 9 in `plan.md`. It captures the remaining work needed to call Blackbird supportable, secure, reproducible, and straightforward to install. It does not assume that an item is complete merely because related code exists; every story must satisfy its acceptance criteria in CI or a recorded release check.

## Priority and release policy

- **P0 — release blocker:** required for the first supported release.
- **P1 — should ship:** expected for v1.0 unless explicitly deferred with a documented workaround.
- **P2 — follow-up:** useful after the first stable release.
- A story is complete only when implementation, automated tests, operator documentation, and upgrade impact are addressed.

---

## Epic 1 — Product completion and release hardening

Close daily-use gaps and define a stable v1 contract before packaging the application.

### SHIP-1.1 — Finish keyboard workflows (P1)

**As a** power user, **I want** consistent keyboard controls **so that** I can operate large torrent queues efficiently.

Acceptance criteria:

- `/`, Space, Delete, Shift+Delete, Escape, arrow navigation, Shift+arrow range selection, and Ctrl/Cmd+A behave as specified in Epic 10 of `plan.md`.
- Shortcuts do not fire while focus is in an input, textarea, select, or editable element.
- Context-menu shortcut hints exactly match implemented behavior on macOS and Linux keyboards.
- Component or browser tests cover destructive-action confirmation and shortcut suppression.

### SHIP-1.2 — Complete responsive desktop behavior (P1)

**As a** user on a laptop-sized display, **I want** the console to remain usable **so that** I can administer rTorrent without a wide monitor.

Acceptance criteria:

- The interface is fully usable at every viewport width from 900px upward.
- Sidebar and table columns degrade in the order documented in `plan.md`; no controls become unreachable.
- Settings, Add Torrent, Details, and Stats have no clipped controls or unintended page-level horizontal scrolling.
- Browser tests cover representative 900px, 1100px, and 1440px viewports.
- Documentation states that mobile layouts below 900px are unsupported in v1.

### SHIP-1.3 — Unify action feedback and recovery (P0)

**As an** operator, **I want** reliable success and failure feedback **so that** the UI never silently disagrees with rTorrent.

Acceptance criteria:

- One toast/notification system handles action errors, partial batch failures, reconnects, and settings-save errors.
- Optimistic mutations revert when their server operation fails and identify the affected torrent or count.
- Error responses have a stable JSON envelope with a machine-readable code, safe user-facing message, and request correlation ID.
- Duplicate failures from retry loops are coalesced to avoid notification floods.
- Tests cover full failure, partial batch failure, timeout, disconnect, and successful recovery.

### SHIP-1.4 — Freeze and document the v1 API/config contract (P0)

**As a** maintainer, **I want** versioned external contracts **so that** releases can evolve without surprising operators.

Acceptance criteria:

- REST and WebSocket schemas are versioned and documented, including compatibility rules.
- Every YAML key is documented with its default, allowed values, restart/reload behavior, and whether Settings can edit it.
- Unknown config keys fail validation with a useful path and line number.
- Deprecated keys have a warning and at least one documented migration path before removal.
- Golden tests guard representative API payloads and complete config round-trips.

---

## Epic 2 — Secure-by-default deployment

Make an installed Blackbird safe to expose to a trusted network and difficult to misconfigure accidentally.

### SEC-2.1 — Enforce production authentication (P0)

**As an** operator, **I want** authentication enabled by default **so that** a fresh deployment is not an open rTorrent control surface.

Acceptance criteria:

- API, WebSocket, and static app routes share one enforced authentication policy.
- Startup fails in production mode when credentials are absent or use an insecure placeholder; an explicit development-only override is available and loudly logged.
- Passwords are stored as bcrypt hashes and can be generated through a documented non-interactive command.
- Authentication failures are rate-limited and logged without credentials or authorization headers.
- Tests cover HTTP requests, WebSocket upgrades, bad credentials, lockout behavior, and log redaction.

### SEC-2.2 — Define network and TLS boundaries (P0)

**As an** operator, **I want** clear network defaults **so that** only intended ports and services are exposed.

Acceptance criteria:

- The recommended deployment binds Blackbird to localhost or a private interface unless the operator deliberately changes it.
- rTorrent SCGI is never published to the host in the default container deployment.
- Documentation provides supported TLS termination examples and secure proxy headers.
- The server applies safe timeouts, request-size limits, origin checks for WebSocket connections, and security headers.
- A security test verifies that anonymous and cross-origin mutation requests are rejected.

### SEC-2.3 — Harden destructive file operations (P0)

**As an** operator, **I want** removal and move operations constrained to managed data roots **so that** a bad daemon path cannot delete unrelated host data.

Acceptance criteria:

- Remove-with-data resolves symlinks and canonical paths before checking configured download roots.
- Files outside allowed roots, filesystem roots, empty paths, and ambiguous multi-file base paths are refused.
- Container filesystem permissions prevent Blackbird from writing outside configuration, session, and download mounts.
- Audit logs include actor, torrent hash, canonical target, action, outcome, and correlation ID.
- Unit and integration tests cover traversal, symlink escape, missing targets, and valid nested data removal.

### SEC-2.4 — Add dependency and image security checks (P1)

**As a** maintainer, **I want** automated supply-chain checks **so that** known vulnerabilities do not silently ship.

Acceptance criteria:

- CI scans Go modules, npm dependencies, container images, and repository secrets.
- Release images run as a non-root user with a read-only root filesystem where practical and no unnecessary Linux capabilities.
- Each release publishes an SBOM and vulnerability scan result.
- Critical or high findings block release unless a time-limited, documented exception is approved.

---

## Epic 3 — Docker Compose rTorrent + Blackbird appliance

Provide a reproducible two-service deployment that builds rTorrent and Blackbird together and runs on Docker Desktop for macOS and Docker Engine for Linux.

### PKG-3.1 — Build a production Blackbird image (P0)

**As an** operator, **I want** a small reproducible Blackbird container **so that** I can deploy the same artifact on any supported host.

Acceptance criteria:

- A multi-stage `Dockerfile` builds the SolidJS frontend and Go binary from a clean checkout.
- The runtime image contains only the binary, CA certificates, timezone data if required, licenses, and an unprivileged runtime user.
- Builds support `linux/amd64` and `linux/arm64`; version, commit, and build date are stamped into the binary.
- The image has an OCI health check and labels for source, revision, version, and license.
- Build context excludes source artifacts, `node_modules`, local configs, downloads, and secrets through `.dockerignore`.

### PKG-3.2 — Build a pinned rTorrent image (P0)

**As an** operator, **I want** rTorrent built from pinned sources **so that** the appliance does not depend on an opaque third-party image.

Acceptance criteria:

- `deploy/docker/rtorrent/Dockerfile` builds pinned libtorrent and rTorrent versions from verified source checksums or signatures.
- The image supports `linux/amd64` and `linux/arm64`, runs as a configurable non-root UID/GID, and exposes no public management port.
- The supplied `.rtorrent.rc` enables an SCGI TCP endpoint only on the private Compose network and stores session state in a persistent mount.
- Downloads, watch directory, and session state have separate documented mount points.
- Image tests verify rTorrent starts, reports expected versions, writes its session, and responds through SCGI.

### PKG-3.3 — Compose the complete appliance (P0)

**As an** operator, **I want** one Compose command to build and start both services **so that** installation is predictable.

Acceptance criteria:

- `docker compose up --build -d` builds and starts `rtorrent` and `blackbird` from the repository.
- Blackbird connects to rTorrent over an internal-only Compose network using `tcp://rtorrent:<port>`; the SCGI port is not mapped to the host.
- Only the Blackbird HTTP port is published by default.
- Named volumes preserve downloads, watch data, rTorrent session state, and Blackbird configuration across recreation.
- Health checks and `depends_on` health conditions prevent Blackbird from being considered ready before rTorrent is accepting SCGI calls.
- Restart policies recover both services after a daemon crash or host reboot.
- `docker compose down` preserves data; destructive cleanup requires a separately documented explicit command.

### PKG-3.4 — Support macOS and Linux host storage (P0)

**As a** macOS or Linux user, **I want** documented storage options **so that** permissions and performance are predictable on my host.

Acceptance criteria:

- The default Compose file uses named volumes and works unchanged on Docker Desktop for macOS and Docker Engine on Linux.
- A Linux override demonstrates bind mounts and maps a configurable `PUID`/`PGID` without requiring world-writable directories.
- A macOS override documents file-sharing requirements and the performance trade-offs of bind mounts versus named volumes.
- Paths containing spaces are covered in the macOS verification run.
- Automated or recorded smoke checks pass on Apple Silicon macOS, x86_64 Linux, and arm64 Linux.

### PKG-3.5 — Generate secrets and first-run configuration (P0)

**As a** first-time operator, **I want** a safe bootstrap flow **so that** I do not hand-edit hashes or bake secrets into images.

Acceptance criteria:

- A documented bootstrap command creates a local config directory, a random password or bcrypt hash, and a minimal valid `config.yml` without committing secrets.
- Compose consumes secrets from a gitignored environment file or Docker secrets-compatible files.
- Generated permissions allow only the intended host user and container user to read credentials.
- Re-running bootstrap is idempotent and does not replace an existing config or password without an explicit rotate command.
- Startup output gives the local URL and next steps without printing the password after initial creation.

### PKG-3.6 — Prove appliance lifecycle and recovery (P0)

**As an** operator, **I want** the Compose stack tested as a unit **so that** upgrades and restarts do not lose data.

Acceptance criteria:

- A CI smoke test builds the two images, starts the stack, waits for health, authenticates, and loads a session snapshot.
- The test adds a deterministic `.torrent`, starts/stops it, changes a label and file priority, and removes it.
- Recreating both containers preserves downloads, labels, configuration, and rTorrent session state.
- Killing rTorrent causes Blackbird to show disconnected state; restarting it restores the live session automatically.
- Upgrade and rollback tests run between two fixture image versions using the same persistent volumes.

---

## Epic 4 — Native binary and service packaging

Support operators who already run rTorrent on the host and do not want containers.

### PKG-4.1 — Publish cross-platform release archives (P0)

**As an** operator, **I want** downloadable versioned binaries **so that** installation does not require Go or Node.js.

Acceptance criteria:

- CI publishes archives for Linux amd64/arm64 and macOS amd64/arm64 from a clean tagged commit.
- Each archive contains the Blackbird binary, annotated config example, license, notices, and concise install/upgrade instructions.
- Filenames include product, semantic version, OS, and architecture.
- SHA-256 checksums are published and verified in the release workflow.
- `blackbird --version` matches the release tag, commit, and reproducible build metadata.

### PKG-4.2 — Provide host service definitions (P1)

**As a** host administrator, **I want** supported service templates **so that** Blackbird starts reliably after reboot.

Acceptance criteria:

- A hardened systemd unit is provided for Linux with an unprivileged user, explicit writable paths, restart policy, and startup ordering guidance for rTorrent.
- A launchd plist example is provided for macOS with equivalent config/log paths.
- Install, start, stop, status, log inspection, and uninstall steps are documented.
- Service examples pass a clean-machine installation smoke test.

### PKG-4.3 — Define filesystem layout and migration rules (P0)

**As an** operator, **I want** stable paths and migrations **so that** upgrades do not overwrite configuration or session data.

Acceptance criteria:

- Supported config, state, cache, and log locations are documented for containers, Linux, and macOS.
- The binary never rewrites the shipped example file and atomically saves operator config.
- Config schema changes are detected at startup and either migrated safely or rejected with actionable instructions.
- Backup and restore procedures cover Blackbird config plus rTorrent session metadata.
- Downgrade behavior is documented for any release that changes persisted state.

### PKG-4.4 — Add package-manager recipes (P2)

**As an** operator, **I want** familiar installation commands **so that** Blackbird fits existing update workflows.

Acceptance criteria:

- A Homebrew formula installs supported macOS binaries and a sample service configuration.
- A Linux package or repository strategy is selected and documented; initial support may be `.deb`/`.rpm` artifacts or a maintained community repository.
- Package installation does not overwrite existing config, and uninstall preserves operator data unless explicitly purged.

---

## Epic 5 — Automated verification and release CI

Turn the existing unit tests and fake daemon into repeatable quality gates, including a real rTorrent seam test.

### QA-5.1 — Establish the required CI matrix (P0)

**As a** maintainer, **I want** every change checked consistently **so that** broken artifacts cannot reach a release.

Acceptance criteria:

- Pull requests run Go formatting, vet/static analysis, Go tests with race detection, frontend type checking, frontend tests, and production builds.
- CI covers supported Go and Node versions and validates dependency lockfiles are reproducible.
- Tests run on Linux amd64; architecture-sensitive container builds also run or emulate arm64.
- Required checks and branch protection are documented.
- Build logs and test reports are retained long enough to diagnose a failed release.

### QA-5.2 — Test against a real rTorrent daemon (P0)

**As a** maintainer, **I want** contract tests against real rTorrent **so that** fake-daemon assumptions do not drift.

Acceptance criteria:

- A hermetic integration target starts the pinned rTorrent image with isolated temporary volumes.
- Tests cover SCGI connection, version discovery, list, add via magnet and file, start/pause/stop, label, directory, tracker, file priority, recheck, and removal.
- Tests assert behavior for both supported rTorrent/libtorrent version combinations if more than one is supported.
- Fixtures are legal to redistribute, deterministic, and do not contact public trackers.
- Integration failures preserve daemon and Blackbird logs as CI artifacts.

### QA-5.3 — Add browser end-to-end release smoke tests (P0)

**As a** maintainer, **I want** critical workflows exercised in a browser **so that** API and UI changes ship together.

Acceptance criteria:

- Tests cover login, connection recovery, filtering, multi-select, start/stop, add torrent, detail tabs, settings save/revert, and Stats.
- The suite runs against the Compose appliance rather than mocked HTTP responses for at least one CI job.
- Browser console errors, failed network requests, and unhandled promise rejections fail the test.
- Screenshots and traces are retained on failure.
- One command reproduces the suite locally.

### QA-5.4 — Add performance and longevity checks (P1)

**As an** operator with a large session, **I want** predictable resource usage **so that** Blackbird can run continuously.

Acceptance criteria:

- A generated 500+ torrent session validates table responsiveness and poll/delta processing budgets.
- A soak test covers reconnects, WebSocket churn, detail focus changes, and settings reads for at least several hours.
- Memory, goroutine, connection, and browser heap growth have documented acceptable thresholds.
- API payload size and latency budgets are recorded and regression-tested.

---

## Epic 6 — Operability, health, and data safety

Give operators enough visibility and recovery tooling to run Blackbird unattended.

### OPS-6.1 — Add liveness, readiness, and diagnostics (P0)

**As an** orchestrator or operator, **I want** meaningful health endpoints **so that** failures can be detected automatically.

Acceptance criteria:

- Liveness reports whether the Blackbird process can serve requests without depending on rTorrent.
- Readiness reports configuration validity and rTorrent reachability, with no credentials or paths leaked.
- Health endpoints have documented authentication/network behavior and stable machine-readable output.
- The status surface distinguishes Blackbird healthy/rTorrent unavailable from Blackbird unavailable.
- Container and service health checks use these endpoints and have tested timeout/retry behavior.

### OPS-6.2 — Standardize logs and correlation (P0)

**As an** operator, **I want** useful structured logs **so that** I can diagnose failures without exposing secrets.

Acceptance criteria:

- Logs are structured JSON or text selected by config and include timestamp, severity, component, action, and request ID.
- Authentication material, magnet query parameters, raw torrent contents, and sensitive filesystem details are redacted where appropriate.
- Normal polling does not flood info-level logs.
- Container logs go to stdout/stderr; native service log guidance includes rotation.
- A troubleshooting guide maps common UI errors to relevant log fields.

### OPS-6.3 — Provide backup, restore, and disaster-recovery procedures (P0)

**As an** operator, **I want** tested recovery instructions **so that** a host or volume failure does not destroy session metadata.

Acceptance criteria:

- Documentation identifies exactly which Blackbird config, secrets, rTorrent session, watch, and download data must be backed up.
- A quiesced backup and restore procedure is tested with the Compose stack.
- Restoring to a new host preserves torrents, labels, paths, and authentication configuration.
- The procedure explains path remapping when host storage locations change.

### OPS-6.4 — Handle graceful upgrades and shutdowns (P1)

**As an** operator, **I want** upgrades without corrupted writes **so that** routine maintenance is safe.

Acceptance criteria:

- SIGTERM stops accepting mutations, closes WebSockets cleanly, finishes or cancels in-flight RPCs, and exits within the container grace period.
- Atomic config writes survive process termination and full disks without replacing a valid file with a partial one.
- Release documentation defines supported upgrade paths and rollback prerequisites.
- Compose and native-service upgrade smoke tests verify a running torrent session is preserved.

---

## Epic 7 — Documentation and onboarding

Make first install, routine use, troubleshooting, and contribution possible without reading the source.

### DOC-7.1 — Write the operator README (P0)

**As a** prospective user, **I want** a concise supported-install overview **so that** I can decide how to run Blackbird.

Acceptance criteria:

- README explains what Blackbird is, current release status, screenshots, supported rTorrent/libtorrent versions, and supported OS/architectures.
- Quick starts cover Docker Compose and an existing host rTorrent installation.
- The document links to configuration, security, upgrade, backup, and troubleshooting guides.
- Limitations and intentionally unsupported v1 features are explicit.

### DOC-7.2 — Publish an annotated configuration reference (P0)

**As an** operator, **I want** every setting explained **so that** I can maintain configuration without guessing.

Acceptance criteria:

- The reference is generated from or tested against the config schema to prevent stale keys.
- It includes minimal, Compose, host-SCGI, TLS-proxy, label/directory, and tuning examples.
- Sensitive settings explain recommended secret handling.
- Every reloadable field is marked as live, reconnect-required, or restart-required.

### DOC-7.3 — Write troubleshooting and support policy (P0)

**As an** operator, **I want** actionable diagnostic guidance **so that** common failures can be resolved quickly.

Acceptance criteria:

- Guide covers SCGI connectivity, authentication loops, WebSocket proxying, permissions, full disks, stale sessions, port/DHT issues, and macOS Docker mounts.
- A documented diagnostics command reports versions and safe health/config summaries without secrets.
- The support policy defines supported versions, security-reporting channel, and the information required in bug reports.

### DOC-7.4 — Create contributor and architecture guides (P1)

**As a** contributor, **I want** a reproducible development workflow **so that** I can make and verify changes safely.

Acceptance criteria:

- One guide covers prerequisites, fake daemon, frontend/backend development, tests, linting, and Compose integration tests.
- An architecture guide documents the SCGI client, poller/cache, REST/WebSocket contracts, embedded frontend, and configuration persistence.
- Pull-request and commit expectations include tests and user-visible release notes.

---

## Epic 8 — Release engineering and project governance

Create traceable, repeatable releases with the legal and maintenance metadata expected of a distributable project.

### REL-8.1 — Define versioning and changelog policy (P0)

**As an** operator, **I want** predictable versions and upgrade notes **so that** I can assess release risk.

Acceptance criteria:

- Semantic versioning rules define compatibility for config, API/WebSocket, persisted state, and container tags.
- A maintained changelog separates breaking changes, features, fixes, security updates, and migration steps.
- Pre-release tags are distinguishable and never replace the stable container tag.
- The application and UI expose the same build version.

### REL-8.2 — Automate signed release publication (P0)

**As an** operator, **I want** verifiable artifacts **so that** I can trust what I install.

Acceptance criteria:

- A tag-triggered workflow builds binaries and multi-architecture images exactly once from the tagged commit.
- Images are published with immutable semantic-version and digest references plus documented moving tags.
- Archives, checksums, images, and SBOM attestations are signed using a documented verification workflow.
- Publishing requires all P0 CI, integration, Compose, and vulnerability gates to pass.
- A dry-run release can be executed without publishing public artifacts.

### REL-8.3 — Add license and third-party notices (P0)

**As a** distributor, **I want** complete licensing metadata **so that** releases can be redistributed legally.

Acceptance criteria:

- The repository declares a project license approved by the owners.
- Binary archives and container images include the project license and required third-party notices.
- rTorrent/libtorrent source-build and redistribution obligations are reviewed and documented.
- CI checks that release bundles contain the expected legal files.

### REL-8.4 — Define the v1 release checklist (P0)

**As a** release manager, **I want** an auditable checklist **so that** “ready to ship” has one meaning.

Acceptance criteria:

- The checklist records supported platforms, clean-install results, upgrade/rollback results, security scans, backups, documentation review, and known issues.
- Every P0 story in this backlog is complete or has an owner-approved blocking rationale; P1 deferrals are listed in release notes.
- A release candidate runs for a documented soak period against a real rTorrent session.
- The final release is tested from the exact published binary archives and image digests, not only from local builds.

---

## Suggested delivery order

1. **Epics 1–2:** freeze behavior and close security blockers.
2. **Epic 3:** deliver the Docker Compose appliance and use it as the reference integration environment.
3. **Epic 5:** make the Compose and real-rTorrent verification mandatory in CI.
4. **Epics 4 and 6:** publish native artifacts and operational safeguards.
5. **Epics 7–8:** complete documentation, licensing, signing, and the v1 release process.

## v1 ship gate

Blackbird is shippable when all P0 stories are complete, the exact release artifacts pass the clean-install and upgrade checks on the supported macOS/Linux matrix, the Compose stack survives recreation without data loss, no unresolved critical/high security findings remain, and the release checklist is signed off with known limitations documented.
