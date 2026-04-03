# Section3 — Service Supervisor

A minimal Go service supervisor for Tachikoma. Fork+exec services from `/workspace/etc/sv/<name>/run`, restart on crash, handle logs.

## Design

- **Binary name:** `section3`
- **Config:** no config file. Service discovery from `/workspace/etc/sv/<name>/run`
- **Log location:** `/workspace/logs/<name>/current` (one file per service, append-only, rotated by supervisor)
- **Entry point:** runs in foreground as PID 1 of the container

## Service Definition

```
/workspace/etc/sv/<name>/
  run          # executable script, exec'd by supervisor
  log/         # (optional) if exists, used for log rotation config
```

## Startup

1. Scan `/workspace/etc/sv/` for subdirectories with `run` files
2. Sort alphabetically (use `1-name`, `2-name` prefixes for ordering)
3. Fork+exec each service in order with small stagger (100ms)
4. Wait for each to become ready before starting next (optional: configurable)
5. Block in supervisor loop

## Supervisor Loop

- Monitor all supervised processes
- On crash: restart with exponential backoff (min 1s, max 60s, multiplier 2x)
- On SIGTERM: stop all services gracefully (SIGTERM, wait 5s, SIGKILL)
- On SIGHUP: reload (re-scan `/workspace/etc/sv/`)

## Log Handling

- Capture stdout + stderr of each service
- Write to `/workspace/logs/<name>/current`
- Rotate when file exceeds 1MB (rename to `current.<timestamp>`, start new `current`)
- Keep last 5 rotated files per service

## Commands

```
section3 start <name>     Start a service
section3 stop <name>      Stop a service
section3 restart <name>   Restart a service
section3 status           Show status of all services
section3 status <name>    Show status of one service
section3 tail <name>     Tail logs (last 20 lines)
section3 help             Show help
```

## Status Output Format

```
$ section3 status
signalshell  running  PID 12345  uptime 2h34m
voice        running  PID 12346  uptime 2h34m
memory-watch stopped  exit 1    last restart 10s ago
```

## Exit Codes

- 0: success (status printed)
- 1: service failed to start
- 2: invalid command

## Future Extensibility

- Exponential backoff per service (via `backoff` file in service dir)
- Notification webhook on crash (via `notify` file in service dir)
- Log filter (via `filter` script that processes each log line)
- Health check (via `check` script that supervisor polls)
