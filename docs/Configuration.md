# Configuration

Like Prometheus: what needs a restart is a command line flag; what can change at runtime is in the
config file.

## Flags

| Flag | Default | In the image |
|---|---|---|
| `--config.file` | `/etc/imageadvisor/config.yml` | `/data/config.yml` |
| `--docker.socket` | `/var/run/docker.sock` | default |
| `--web.listen-address` | `:8080` (changes with 1.0) | default |
| `--log.level` | `info` (`debug`, `info`, `warn`, `error`) | default |
| `--log.format` | `logfmt` (`logfmt`, `json`) | default |
| `--version` | print the version and exit | |

An unknown flag or an invalid value exits with code 2.

## Config file

Everything is optional; a missing file means the defaults. ImageAdvisor never writes the file.

```yaml
global:
  check_interval: 24h              # time between two checks
```

### Loading and reloading

- Unknown keys are an error.
- An invalid file at start: ImageAdvisor exits with code 1.
- The file is re-read when it changed (checked before every check), on `SIGHUP` and on
  `POST /-/reload`. An invalid file then keeps the previous config and sets
  `imageadvisor_config_last_reload_successful` to 0; the log says why.
