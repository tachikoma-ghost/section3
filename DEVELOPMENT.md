# Section3 — Development

## Architecture

```
section3
├── Discover()      → scan /workspace/etc/sv/<name>/run
├── StartAll()      → fork+exec each service with stagger
├── Service.wait()   → monitor process, restart on exit
└── StopAll()        → SIGTERM → SIGKILL cascade
```

## Service Lifecycle

1. `Start()` — fork+exec, capture stdout/stderr to log file, launch goroutine to `Wait()`
2. `wait()` — on exit, apply backoff, restart after delay
3. `Stop()` — set `stopped=true`, send SIGTERM to process group, wait 5s, SIGKILL

## Process Groups

Each service runs in its own process group (`Setpgid: true`). This allows clean shutdown of all child processes when stopping a service.

## Log Rotation

Currently: append-only to `current`. Rotation not yet implemented.

## Future Extensibility

See `SPEC.md` — "Future Extensibility" section for planned features.
