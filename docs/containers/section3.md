<!-- L2: Container -->
# Section3

Single Go binary (`src/section3/main.go`). Runs as the init/supervisor process for the Tachikoma workspace. Reads YAML config, forks services, monitors them, and restarts on failure.

## Responsibility

Start, stop, restart, and monitor all configured services. Rotate logs. Provide a CLI for status inspection and service control. Run continuously as PID 1 of the workspace process tree.

## Technology

Go, `os/exec`, process groups via `Setpgid`. Binary at `src/section3/bin/section3`.

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
section3 help          Show help
```

### Config file
`/workspace/section3.yml` — see [config schema](../specs/config.md) for full format.

### Log files
Per-service log files at `/tmp/section3-logs/<name>.log`. Rotation at 1MB, keeps last 5 files (`<name>.log.1` ... `<name>.log.5`).

### Signals
- **SIGTERM** — graceful stop (send to all services, wait 5s, then SIGKILL)
- **SIGHUP** — reload config (re-read `section3.yml`, apply changes without restarting running services)

## Key decisions

- **Process groups** — each service in its own process group so stopping a service kills all its children
- **Restart backoff** — exponential (1s → 2s → 4s → ... → max 60s), resets on successful start
- **Config reload** — `section3 reload` re-reads `section3.yml` without restarting already-running services; new services are started, removed services are stopped, existing services are carried over
- **Alphabetical startup** — services start in sorted order (not dependency order)
- **Per-service working directory** — each service can specify `dir:` to set its working directory; services without explicit `dir` use `defaults.dir`

## Code pointers

- Config loading: `LoadConfig()` in `src/section3/main.go`
- Service lifecycle: `Service.Start()`, `Service.Stop()`, `Service.wait()` in `src/section3/main.go`
- Supervisor loop: `runDaemon()` in `src/section3/main.go`
- Log rotation: `Service.OpenLog()`, `Service.checkRotation()` in `src/section3/main.go`
- Socket CLI: `serveSocket()`, `handleConn()` in `src/section3/main.go`