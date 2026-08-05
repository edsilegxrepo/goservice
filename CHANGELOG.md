# Changelog

All notable changes to `goservice` are documented in this file.

Format: `vX.Y.Z - YYYY-MM-DD`, with changes grouped under a dated section per revision.

> **Note:** This project has no git history (the working tree is not a git
> repository), so `v0.0.0` is a reconstructed baseline describing the current
> state of the codebase as of the date below. Future revisions should be
> appended as new dated sections.

## [v0.0.0] - 2026-08-04

### Added

- Cross-platform service library (`github.com/edsilegxrepo/goservice`) with a
  platform-neutral public API: `Install`, `Remove`, `Run`, `IsService`,
  `IsServiceErr`, `Config`, `Options`, `Runner`/`RunnerFactory`, and `Logger`.
- Windows backend: Service Control Manager install/remove/run, Windows Event
  Log integration with console fallback, and graceful shutdown driven by SCM
  stop/shutdown control requests.
- Linux backend: systemd unit generation from an embedded or custom template,
  install/remove via `systemctl` with sudo escalation, and unit-file writes
  through `tee` with symlink-escape protection.
- CLI (`cmd/service`): `-install` / `-remove` / `-run` modes, automatic
  run-mode detection when started by the SCM, granular exit codes (0-6),
  `-args` quoted-string parsing, and environment-based password handling with
  post-run secret scrubbing.
- Input validation with sentinel errors: `ErrInvalidConfig`,
  `ErrAlreadyExists`, `ErrNotInstalled`, `ErrPermissionDenied`.
- Configurable service stop timeout and poll interval via `Options`
  (`StopTimeout`, `StopPollInterval`) with safe defaults.
- Dependency-injected platform seams (manager backend, Windows SCM/Event Log
  deps, privileged Linux operations, CLI service ops) enabling fully fake-based
  unit tests with no host mutation.
- Documentation: `README.md`, `ARCHITECTURE.md`, `TESTING.md`, and this
  changelog.

### Changed

- Module and package renamed to `github.com/edsilegxrepo/goservice`
  (`goservice`) for a consistent, user-friendly import path.

### Fixed

- Unchecked errors from `Close`/`Disconnect` calls are now propagated or
  explicitly discarded (errcheck, gosec G104).
- `WaitHint` conversion from `time.Duration` to `uint32` is clamped to
  `math.MaxUint32` to prevent integer overflow (gosec G115).
- Removed a unit-suffixed variable name on a `time.Duration` value
  (staticcheck ST1011).

### Security

- Static security audit passes clean: gosec 0 issues, golangci-lint 0 issues,
  TruffleHog 0 secrets, Grype/GoVulnCheck no vulnerabilities. The one semgrep
  false positive (`text/template` usage) is suppressed inline with a
  justification: systemd unit files are plain text, and `html/template` would
  corrupt them.

### Tests

- Unit-test suite with fake Windows SCM, fake systemd/privileged operations,
  and end-to-end CLI coverage; Linux tests verified via a WSL-executed test
  binary.
- Statement coverage: 81.3% (root, Windows), 90.2% (CLI), 86.6% (root,
  Linux) — all above the 80% gate.
