<!-- L2: Container -->
# Section3

Single Go binary (`src/section3/`). Runs as the init/supervisor process for the Tachikoma workspace. Reads YAML config, forks services, monitors them, and restarts on failure.

## Responsibility

Start, stop, restart, and monitor all configured services. Rotate logs. Provide a CLI for status inspection and service control. Run continuously as PID 1 of the workspace process tree.

## Technology

Go, `os/exec`, process groups via `Setpgid`. Binary at `bin/section3`.

## Interfaces

- **CLI** — `section3 status`, `section3 start <name>`, `section3 stop <name>`, `section3 restart <name>`, `section3 tail <name>`, `section3 reload`
- **Config file** — `/workspace/section3.yml` — YAML service definitions
- **Log files** — per-service log files with rotation (`current`, `current.1` ... `current.5`, max 1MB each)
- **Signals** — SIGTERM triggers graceful stop; SIGKILL used as fallback after 5s

## Components

- [service-manager](../components/service-manager.md) — fork/exec, wait loop, restart backoff, stop cascade
- [log-rotator](../components/log-rotator.md) — per-service log files, 1MB rotation, keeps last 5

## Key decisions

- **Process groups** — each service in its own process group so stopping a service kills all its children
- **Restart backoff** — not linear; prevents tight restart loops on persistent failures
- **Config reload** — `section3 reload` re-reads `section3.yml` without restarting already-running services
