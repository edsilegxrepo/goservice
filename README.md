# goservice

Cross-platform service installation and runtime helper for Windows (SCM) and Linux (systemd).

## Application overview and objectives

`goservice` provides:

- A small Go library (`goservice`) for installing, removing, and running services.
- A CLI (`goservice`) for installing/removing/running a binary as a service.
- Platform-neutral validation and shutdown semantics.
- Windows Service Control Manager integration and Linux systemd unit generation.

Related docs:

- [ARCHITECTURE.md](ARCHITECTURE.md)
- [TESTING.md](TESTING.md)

## Security assessment

### Encryption in transit

Not applicable. The library and CLI do not open network connections or transmit data.

### Secret management

- Preferred secret source: `SERVICE_PASS` environment variable.
- `-pass` is deprecated because command-line arguments can leak through process listings and shell history.
- `cfg.Pass` and `SERVICE_PASS` are scrubbed after CLI execution on all paths.
- The library does not persist secrets.

### Authentication configuration

- **Windows:** a custom service account can be supplied with `-user` and `SERVICE_PASS`. When `-user` is set on Windows, a password is required before SCM calls.
- **Linux:** `-user` is interpreted as `user` or `user:group` and rendered into the systemd unit as `User=`/`Group=`.

### RBAC

There is no application-level RBAC. Authorization is enforced by the operating system:

- Windows: SCM security descriptors and service account rights.
- Linux: root or sudoers policy for writing `/etc/systemd/system` and running `systemctl`.

### Libraries

- Go module target: `go 1.26`
- Third-party dependency: `golang.org/x/sys v0.47.0`
- No other third-party modules are used.

Run `govulncheck ./...` in CI to confirm the dependency set remains non-vulnerable.

### Privilege context

- The library runs with the privileges of the calling process.
- Install/remove require elevated privileges (SCM access or root/sudo).
- `run` can operate in an unprivileged context when started by the service manager or when the caller already has the required rights.

## Code quality assessment

Current state:

- Platform-specific code is isolated with build tags.
- Public API validates input before OS calls.
- Sentinel errors allow granular CLI exit codes.
- Platform backends are mockable; unit tests do not require admin rights.
- Shutdown is cancellation-based with configurable timeouts.

Best practices followed:

- No shell invocation for privileged commands; arguments are passed directly to `exec`.
- systemd unit fields are escaped; Windows binary paths are quoted.
- Buffered channels prevent goroutine leaks during shutdown.
- Secrets are not logged and are cleared after use.

Remaining improvements:

- Add fuzz tests for `splitArgs`, systemd quoting, and Windows path quoting.
- Add CI coverage for Windows and Linux.
- Unify the Windows and Linux backend implementation style.

## Command line arguments

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `-name` | string | `""` | Service name. Required. |
| `-binary` | string | `""` | Absolute path to the service binary. Required for `-install` and `-run`. |
| `-args` | string | `""` | Arguments passed to the binary, as one quoted string. |
| `-display` | string | `""` | Service display name. Defaults to `-name`. |
| `-description` | string | `""` | Service description. Defaults to `"<name> service"`. |
| `-user` | string | `""` | Windows service user, or Linux `user[:group]`. |
| `-pass` | string | `""` | Deprecated. Windows service user password. Use `SERVICE_PASS`. |
| `-instance` | string | `""` | Linux systemd instance name for templated units. |
| `-template` | string | `""` | Custom Linux systemd unit template path. |
| `-install` | bool | `false` | Install the service. |
| `-remove` | bool | `false` | Remove the service. |
| `-run` | bool | `false` | Run the service. Auto-selected when started by the Windows SCM. |

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Runtime failure |
| `2` | Usage/flag error |
| `3` | Configuration validation failure |
| `4` | Permission denied |
| `5` | Service already exists |
| `6` | Service is not installed |

## Examples

### Install a Windows service

```powershell
$env:SERVICE_PASS = 'secret'
service -install -name demo -binary 'C:\svc\demo.exe' -args '--port 8080' -user '.\svcuser'
```

Sample output:

```text
service demo installed successfully
```

### Install a Linux systemd service

```bash
sudo service -install -name demo -binary /opt/demo/demo -args '--port 8080' -user demo:demo
```

Sample output:

```text
service demo installed successfully
```

### Install a templated Linux instance

```bash
sudo service -install -name demo -instance blue -binary /opt/demo/demo
```

This writes `/etc/systemd/system/demo@.service` and enables `demo@blue`.

### Remove a service

```powershell
service -remove -name demo
```

Sample output:

```text
service demo removed successfully
```

### Run interactively

```powershell
service -run -name demo -binary 'C:\svc\demo.exe' -args '--port 8080'
```

Press `Ctrl+C`; the process exits with code `0` after graceful shutdown.

### Library usage

```go
cfg := goservice.Config{
    Name:       "demo",
    BinaryPath: `C:\svc\demo.exe`,
    Args:       []string{"--port", "8080"},
}
if err := goservice.Install(cfg); err != nil {
    // handle install error
}
```

## Testing

See [TESTING.md](TESTING.md) for unit-test and WSL-based Linux validation instructions.
