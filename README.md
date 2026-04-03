# Section3 — Service Supervisor

Minimal Go service supervisor with YAML config. Fork+exec services, restart on crash, handle logs with rotation.

## Quick Start

### Build
```bash
cd src/section3
make build
```

### Configuration
Edit `/workspace/section3.yml`:

```yaml
services:
  signalshell:
    command: /home/node/.local/bin/signalshell serve
    restart: always

  voice:
    command: /workspace/src/voice/bin/voice -config /workspace/src/voice/config.yaml
    restart: always
    depends_on:
      - signalshell
```

### Run
```bash
# Start supervisor (blocks)
./bin/section3

# Or in background, then check status
./bin/section3 &
./bin/section3 status
```

## Commands
```
section3               Start the supervisor (default when run without args)
section3 status        Show status of all services
section3 status <name> Show status of one service
section3 start <name>  Start a service
section3 stop <name>   Stop a service
section3 restart <name> Restart a service
section3 reload        Reload config (add/remove services)
section3 tail [-n N] [name]  Show last N log lines (default: 20, all if no name)
section3 help          Show this help
```

## Restart Options

- `always` — restart on any exit (default)
- `never` — do not restart

## Log Location

Logs go to `/var/log/section3/<name>.log`

Log rotation: files rotate at 1MB, keeping last 5 versions (`.log.1` ... `.log.5`).

## Tachikoma Integration

In `/workspace/init.sh`:
```bash
#!/bin/bash
exec /workspace/src/section3/bin/section3
```

## Files

```
src/section3/
  main.go         # source
  Makefile        # build
  README.md       # this file
  DEVELOPMENT.md  # architecture notes
  SPEC.md         # design spec
  VERSION         # version
  section3.yml    # sample config
  go.mod
  bin/section3    # compiled binary
```
