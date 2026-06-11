<img src="assets/icon.png" width="80" align="right" />

# Section3 — Service Supervisor

Minimal Go service supervisor with YAML config. Fork+exec services, restart
on crash, handle logs with rotation. A single static binary that works as an
init process for containers or as a lightweight supervisor anywhere else —
no systemd, no runit, one YAML file.

## CLI

```
section3               Start the supervisor (default when run without args)
section3 status        Show status of all services
section3 status <name> Show status of one service
section3 start <name>  Start a service
section3 stop <name>   Stop a service
section3 restart <name> Restart a service
section3 reload        Reload config (add/remove services)
section3 tail [-n N] [name]  Show last N log lines (default: 20, all if no name)
section3 self version  Show binary version
section3 self update   Update the binary to the latest release
section3 help          Show this help
```

The first invocation starts the daemon; every other command talks to it over
a unix socket.

## Configuration

section3 reads `/workspace/section3.yml`:

```yaml
defaults:
  dir: /workspace        # working directory for services that don't set their own
  restart: always        # always | never | on-crash

services:
  web:
    command: /usr/local/bin/my-web-server --port 8080

  worker:
    command: /usr/local/bin/my-worker --queue default
    restart: on-crash
    depends_on:
      - web

  init-once:
    command: /usr/local/bin/migrate-db.sh
    restart: never
    dir: /tmp
```

Restart policies:

- `always` — restart on any exit (default)
- `never` — do not restart
- `on-crash` — restart only on non-zero exit

Restarts back off exponentially up to 60s. `section3 reload` (or SIGHUP)
stops services removed from the config and starts newly added ones; already-
running services are left untouched, so a changed `command` takes effect on
the next `section3 restart <name>`.

## Install

```bash
curl -fsSL https://signalshell.com/install-section3 | sh
```

Downloads the latest release (linux amd64/arm64), verifies the sha256, and
installs to `/usr/local/bin` or `~/.local/bin`.

Update later with:

```bash
section3 self update    # fetch latest release, verify minisign signature, replace binary
```

A running daemon keeps the old version until it is restarted — restarting the
daemon restarts all managed services, so plan that deliberately.

## Run

```bash
# Start supervisor (blocks)
section3

# Or in background, then check status
section3 &
section3 status
```

## Docker

section3 works as the container entrypoint, supervising everything inside:

```dockerfile
RUN curl -fsSL https://signalshell.com/install-section3 | sh
COPY section3.yml /workspace/section3.yml
ENTRYPOINT ["/usr/local/bin/section3"]
```

With docker-compose:

```yaml
services:
  app:
    build: .
    init: true
```

Use `init: true` (or `docker run --init`): section3 reaps its direct
children, but if a service forks and dies, the orphaned grandchildren
reparent to PID 1 — tini handles those.

Lifecycle maps onto Docker naturally:
- `docker stop` → SIGTERM → section3 stops all services gracefully, then exits
- `docker kill -s HUP <ctr>` or `docker exec <ctr> section3 reload` → config reload
- `docker exec <ctr> section3 status` → service status from outside

Alternatively, from an entrypoint script, `exec` it so signals reach the
supervisor directly:
```bash
#!/bin/bash
exec /usr/local/bin/section3
```

## Logs

Logs go to `/tmp/section3-logs/<name>.log`

Output is piped through the supervisor, so logs rotate at 1MB even while a
service is running, keeping last 5 versions (`<name>.log.1` ...
`<name>.log.5`). The daemon's own `section3.log` rotates the same way.

## Building from source

```bash
make build    # → bin/section3
make test
```

## Releasing (maintainer)

```bash
make release
```

Bumps `VERSION`, builds linux amd64/arm64, signs with minisign, uploads to
`signalshell.com/releases/section3/`, commits and tags. The `self update`
command verifies against the public key embedded in `selfupdate.go`.

## Files

```
src/section3/
  main.go         # supervisor source
  selfupdate.go   # self version/update commands
  Makefile        # build + release
  README.md       # this file
  SPEC.md         # design spec
  docs/           # architecture docs
  VERSION         # version
  section3.yml    # example config
  go.mod
  bin/section3    # compiled binary
```
