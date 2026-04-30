# Section3 — Service Supervisor

A minimal Go service supervisor for Tachikoma. Fork+exec services from YAML config, restart on crash, handle logs with rotation.

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
- On crash: restart with exponential backoff (min 1s, max 60s, multiplier 2x)
- On SIGTERM: stop all services gracefully (SIGTERM, wait 5s, SIGKILL)
- On SIGHUP: reload config

## Log Handling

- Capture stdout + stderr of each service
- Write to `/tmp/section3-logs/<name>.log`
- Rotate when file exceeds 1MB (rename to `<name>.log.1`, start new `<name>.log`)
- Keep last 5 rotated files per service

## Commands

```
section3 start <name>     Start a service
section3 stop <name>      Stop a service
section3 restart <name>   Restart a service
section3 reload           Reload config (add/remove services)
section3 status           Show status of all services
section3 status <name>    Show status of one service
section3 tail [-n N] [name]  Tail logs (default: 20 lines, all services if no name)
section3 help             Show help
```

## Status Output Format

```
$ section3 status
signalshell  running  PID 12345  uptime 2h34m
voice        running  PID 12346  uptime 2h34m
```

## Exit Codes

- 0: success (status printed)
- 1: service failed to start
- 2: invalid command

## Future Extensibility

- Notification webhook on crash
- Log filter (script that processes each log line)
- Health check (script that supervisor polls)
