<img src="assets/icon.png" width="80" align="right" />

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
defaults:
  dir: /workspace
  restart: always

services:
  signalshell:
    command: /home/node/.local/bin/signalshell serve

  voice:
    command: /workspace/src/voice/bin/voice -config /workspace/src/voice/config.yaml
    depends_on:
      - signalshell

  one-shot:
    command: /bin/once.sh
    restart: never
    dir: /tmp
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
- `on-crash` — restart only on non-zero exit

## Log Location

Logs go to `/tmp/section3-logs/<name>.log`

Output is piped through the supervisor, so logs rotate at 1MB even while a service is running, keeping last 5 versions (`<name>.log.1` ... `<name>.log.5`). The daemon's own `section3.log` rotates the same way.

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
