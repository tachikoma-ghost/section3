<!-- L1: System Context -->
# Section3

Documentation follows the [Refraction documentation standard v1.1](https://signalshell.com/refraction/v1.1).

Service supervisor for the Tachikoma workspace. Reads a YAML config, starts all configured services, monitors them, and restarts on failure. The init process for all long-running components.

## External actors

- **Developer (Martin / Tachikoma)** — edits `section3.yml`, runs `section3 status/start/stop/restart/reload`
- **Managed services** — telegram bot, location server, voice app, memory watcher, browser server, signalshell, expo apps
- **Operating system** — process groups, signals, file system for logs

## Containers

- [section3](containers/section3.md) — Go binary: config loader, process manager, log rotator, CLI

## Reference

- [Config schema](reference/config-schema.md) — full YAML config format with all options

## Constraints and non-goals

- Not a replacement for systemd — intended for development and personal server use
- `depends_on` is parsed but not yet enforced (services start alphabetically, not in dependency order)
- No health checks beyond process alive/dead
- Logs to files only; no log aggregation or forwarding