<!-- Ops -->
# Development

## Architecture

Main binary at `src/section3/main.go` (~670 lines). No separate packages.

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

- `checkRotation()` called on each log open, checks if file exceeds 1MB
- Rotation: file → file.1, file.N → file.N+1, file.5 deleted
- Keeps last 5 rotated files

## Config

Full schema at [config schema](../specs/config.md).

## Running Tests

```bash
go test ./...
```

## Building

```bash
go build -o bin/section3 .
```