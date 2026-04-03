# Section3 — Development

## Architecture

```
section3
├── LoadConfig()     → read /workspace/section3.yml
├── StartAll()       → fork+exec each service with stagger
├── Service.wait()   → monitor process, restart on exit
└── StopAll()       → SIGTERM → SIGKILL cascade
```

## Service Lifecycle

1. `Start()` — fork+exec via `/bin/sh -c`, capture stdout/stderr to log file, launch goroutine to `Wait()`
2. `wait()` — on exit, check `shouldRestart()`, apply backoff, restart after delay
3. `Stop()` — set `stopped=true`, send SIGTERM to process group, wait 5s, SIGKILL

## Process Groups

Each service runs in its own process group (`Setpgid: true`). This allows clean shutdown of all child processes when stopping a service.

## Log Rotation

- `checkRotation()` — called on each log open, checks if `current` exceeds 1MB
- Rotation: `current` → `current.1`, old `current.N` deleted, new `current` started
- Keeps last 5 files: `current`, `current.1` ... `current.5`

## Config Format

YAML at `/workspace/section3.yml`:
```yaml
services:
  <name>:
    command: string  # shell command to run
    restart: always | never
    depends_on: []string  # parsed but not enforced yet
```
