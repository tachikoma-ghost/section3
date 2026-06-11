# Section3 — Service Supervisor

A minimal Go service supervisor. Fork+exec services from YAML config, restart on crash, handle logs with rotation.

## Design

- **Binary name:** `section3`
- **Config:** YAML file at `/workspace/section3.yml`
- **Log location:** `/tmp/section3-logs/<name>.log` (one file per service, rotated by supervisor)
- **Entry point:** runs in foreground as PID 1 of the container

## Configuration

```yaml
defaults:
  dir: /workspace           # default working directory
  restart: always           # default restart policy

services:
  <name>:
    command: /path/to/executable args...
    dir: /workspace          # working directory (optional)
    restart: always | never | on-crash
    log_max_size: 10M        # log rotation threshold (optional, default 1M)
    depends_on:
      - other-service
```

## Startup

1. Read `/workspace/section3.yml`
2. Sort service names alphabetically
3. Fork+exec each service with small stagger (100ms)
4. Block in supervisor loop

## Supervisor Loop

- Monitor all supervised processes
- On crash: restart with exponential backoff (min 1s, max 60s, multiplier 2x); a run of 60s+ resets the backoff
- On SIGTERM: stop all services gracefully (SIGTERM, wait 5s, SIGKILL)
- On SIGHUP: reload config

## Log Handling

- Capture stdout + stderr of each service via a pipe through the supervisor
- Write to `/tmp/section3-logs/<name>.log`
- Rotate when file would exceed `log_max_size` (default 1MB): rename to `<name>.log.1`, start new `<name>.log`; works mid-run, no restart needed
- Keep last 5 rotated files per service
- The daemon's own `section3.log` rotates by the same rules

## Commands

```
section3 start <name>     Start a service
section3 stop <name>      Stop a service
section3 restart <name>   Restart a service
section3 reload           Reload config (add/remove services)
section3 status           Show status of all services
section3 status <name>    Show status of one service
section3 tail [-n N] [name]  Tail logs (default: 20 lines, all services if no name)
section3 self version     Show binary version
section3 self update      Update the binary to the latest release
section3 help             Show help
```

## Status Output Format

```
$ section3 status
web     running  PID 12345  uptime 2h34m
worker  running  PID 12346  uptime 2h34m
```

## Exit Codes

- 0: success
- 1: error (daemon not running, unknown command or service, failed action)

## Future Extensibility

- Notification webhook on crash
- Log filter (script that processes each log line)
- Health check (script that supervisor polls)
