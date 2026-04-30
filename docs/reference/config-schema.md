<!-- Reference -->
# Section3 Config Schema

Full YAML format for `/workspace/section3.yml`.

## Top-level

```yaml
defaults:
  dir: /workspace            # default working directory for all services
  restart: always             # default restart policy

services:
  <name>:
    command: /path/to/exe     # required; the command to run
    dir: /workspace           # working directory; uses defaults.dir if omitted
    restart: always           # always | never | on-crash
    depends_on:               # parsed but not enforced (see system.md)
      - other-service
```

## Service config

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `command` | string | required | Executable and arguments |
| `dir` | string | `defaults.dir` | Working directory for the service |
| `restart` | string | `defaults.restart` | `always` \| `never` \| `on-crash` |
| `depends_on` | list | none | Service names this depends on (not enforced) |

## Defaults

The `defaults` block sets fallback values for all services. A service with an explicit `dir:`, `restart:`, or `depends_on:` uses that value instead of the default. Omitting the key entirely uses the default.

Example:
```yaml
defaults:
  dir: /workspace
  restart: always

services:
  # Uses dir: /workspace
  signalshell:
    command: /home/node/.local/bin/signalshell serve

  # Overrides dir to /tmp
  temp-service:
    command: /bin/my-script.sh
    dir: /tmp

  # Does not restart
  one-shot:
    command: /bin/once.sh
    restart: never
```

## Restart policies

- **`always`** — restart on any exit, including clean exit
- **`never`** — do not restart; service runs once and stops
- **`on-crash`** — restart only on non-zero exit code

## Log files

Log files are written to `/tmp/section3-logs/<service-name>.log`. When a log file exceeds 1MB, it is rotated:

- `<name>.log` → `<name>.log.1`
- `<name>.log.1` → `<name>.log.2`
- ...
- `<name>.log.4` → `<name>.log.5`
- `<name>.log.5` is deleted

A maximum of 5 rotated files are kept per service.