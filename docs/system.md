<!-- L1: System Context -->
# Section3

Documentation follows the [Refraction documentation standard v1.1](https://signalshell.com/refraction/v1.1).

Service supervisor in a single static binary. Reads a YAML config, starts all configured services, monitors them, and restarts on failure. Designed to run as the init process of a container that hosts more than one long-running process.

## External actors

- **Operator** — edits `section3.yml`, runs `section3 status/start/stop/restart/reload`
- **Managed services** — the long-running processes defined in the config
- **Release server** — `signalshell.com/releases/section3/` serves signed binaries for `section3 self update` and the install script
- **Operating system** — process groups, signals, file system for logs

## Containers

- [section3](containers/section3.md) — Go binary: config loader, process manager, log rotator, CLI

## Ops

- [Development](../ops/development.md) — architecture walkthrough, testing, building

## Specs

- [Config schema](specs/config.md) — YAML config format with all options

## Constraints and non-goals

- Not a replacement for systemd — intended for development and personal server use
- `depends_on` is parsed but not yet enforced (services start alphabetically, not in dependency order)
- No health checks beyond process alive/dead
- Logs to files only; no log aggregation or forwarding