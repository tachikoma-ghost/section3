<!-- L2: Container -->
# Section3

Single Go binary (`main.go` + `selfupdate.go`). Runs as the init/supervisor process of its container or machine. Reads YAML config, forks services, monitors them, and restarts on failure.

## Responsibility

Start, stop, restart, and monitor all configured services. Rotate logs. Provide a CLI for status inspection and service control. Run continuously as PID 1 of the workspace process tree.

## Technology

Go, `os/exec`, process groups via `Setpgid`. Binary at `bin/section3`.

## Interfaces

### CLI
```
section3               Start the supervisor daemon
section3 status        Show status of all services
section3 status <name> Show status of one service
section3 start <name>  Start a service
section3 stop <name>   Stop a service
section3 restart <name> Restart a service
section3 reload        Reload config (add/remove services)
section3 tail [-n N] [name]  Show last N log lines (default: 20)
section3 self version  Show binary version
section3 self update   Update the binary to the latest release
section3 help          Show help
```

The `self` commands run client-side and never reach the daemon; all other
unknown subcommands are forwarded to it over the unix socket.

### Config file
`/workspace/section3.yml` — see [config schema](../specs/config.md) for full format.

### Log files
Per-service log files at `/tmp/section3-logs/<name>.log`. Output is piped through the supervisor and rotates at `log_max_size` (default 1MB) even mid-run, keeping last 5 files (`<name>.log.1` ... `<name>.log.5`). The daemon's `section3.log` rotates the same way at the 1MB default.

### Release endpoint
`self update` fetches `https://signalshell.com/releases/section3/latest.json`,
downloads the platform binary, verifies its minisign signature against the
public key embedded in `selfupdate.go`, and replaces itself atomically.

### Signals
- **SIGTERM** — graceful stop (send to all services, wait 5s, then SIGKILL)
- **SIGHUP** — reload config (re-read `section3.yml`, apply changes without restarting running services)

## Key decisions

- **Process groups** — each service in its own process group so stopping a service kills all its children
- **Restart backoff** — exponential (1s → 2s → 4s → 8s → ... → max 60s). A run of at least 60s counts as recovery and resets the backoff, so the first crash after a long healthy stretch restarts after 1s; explicit `stop`/`restart` also resets it
- **Config reload** — `section3 reload` re-reads `section3.yml` without restarting already-running services; new services are started, removed services are stopped, existing services are carried over
- **Alphabetical startup** — services start in sorted order (not dependency order)
- **Per-service working directory** — each service can specify `dir:` to set its working directory; services without explicit `dir` use `defaults.dir`

## Code pointers

- Config loading: `LoadConfig()` in `main.go`
- Service lifecycle: `Service.Start()`, `Service.Stop()`, `Service.wait()` in `main.go`
- Supervisor loop: `runDaemon()` in `main.go`
- Log rotation: `rotatingWriter` in `main.go`
- Socket CLI: `serveSocket()`, `handleConn()` in `main.go`
- Self-update: `runSelf()`, `verifyMinisign()`, `selfReplace()` in `selfupdate.go`

## Contributing

See [development](../ops/development.md) for contributor documentation (architecture walkthrough, testing, building).