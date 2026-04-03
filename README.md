# Section3 — Service Supervisor

Minimal Go service supervisor. Fork+exec services from `/workspace/etc/sv/<name>/run`, restart on crash, handle logs.

## Quick Start

### Build
```bash
cd src/section3
make build
```

### Add a Service
```bash
mkdir -p /workspace/etc/sv/my-service
echo '#!/bin/bash
exec /path/to/my-binary --flags' > /workspace/etc/sv/my-service/run
chmod +x /workspace/etc/sv/my-service/run
```

### Run
```bash
# Start supervisor (blocks)
./bin/section3

# Or in background, then check status
./bin/section3 &
./bin/section3 status
```

### Commands
```
section3                  Start the supervisor (default when run without args)
section3 status           Show status of all services
section3 status <name>  Show status of one service
section3 start <name>    Start a service
section3 stop <name>     Stop a service
section3 restart <name>  Restart a service
section3 tail [-n N] [name]  Show last N log lines (default: 20, all if no name)
section3 help             Show this help
```

## Log Location

Logs go to `/workspace/logs/<name>/current`

Example:
```
/workspace/logs/signalshell/current
/workspace/logs/voice/current
```

## Ordering

Services start alphabetically. Use numeric prefixes for ordering:

```
/workspace/etc/sv/1-signalshell/run
/workspace/etc/sv/2-voice/run
```

## Crash Recovery

On crash, section3 restarts with exponential backoff:
- 1s → 2s → 4s → ... → max 60s

## Tachikoma Integration

In `/workspace/init.sh`:
```bash
#!/bin/bash
exec /workspace/src/section3/bin/section3
```

## Files

```
src/section3/
  main.go      # source
  Makefile     # build
  SPEC.md      # design spec
  VERSION      # version
  bin/section3 # compiled binary
```
