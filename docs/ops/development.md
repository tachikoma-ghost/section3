<!-- Ops -->
# Development

## Architecture

Single `main` package: `main.go` (supervisor, CLI, log rotation) and
`selfupdate.go` (`self version`/`self update`).

Key functions:
- `LoadConfig()` — parse `section3.yml`
- `StartAll()` — fork+exec each service with 100ms stagger
- `Service.Start()` — fork+exec via `/bin/sh -c`, capture stdout/stderr to log file, launch goroutine to `Wait()`
- `Service.wait()` — monitor process, restart on exit with backoff
- `Service.Stop()` — set stopped flag, send SIGTERM to process group, wait 5s, SIGKILL

## Service Lifecycle

1. `Start()` — fork+exec, capture stdout/stderr to log, launch goroutine for Wait
2. `wait()` — on exit, check `shouldRestart()`, apply backoff, restart after delay
3. `Stop()` — set stopped=true, SIGTERM to process group, wait 5s, SIGKILL

## Process Groups

Each service runs in its own process group (`Setpgid: true`). This allows clean shutdown of all child processes when stopping a service.

## Log Rotation

- Service output is piped through a `rotatingWriter` owned by the supervisor; the child never holds the log fd, so rotation works while the service runs
- `rotatingWriter.Write` rotates before any write that would push the file past 1MB: file → file.1, file.N → file.N+1, file.5 overwritten
- Keeps last 5 rotated files; rename/reopen errors are logged, with a 30s cooldown so a persistent failure can't recurse through the daemon's own log
- The writer survives crash restarts and is closed on `Stop()` or a no-restart exit

## Config

Full schema at [config schema](../specs/config.md).

## Running Tests

```bash
go test ./...
```

## Building

```bash
make build
```

`make build` embeds version, commit, and build time via `-ldflags`; a plain
`go build` works but `self version` then reports `dev`. Releases are built
with `make release` (see the README).