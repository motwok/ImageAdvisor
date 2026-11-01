# Configuration

Like Prometheus: what needs a restart is a command line flag; what can change at runtime is in the
config file.

## Flags

| Flag | Default | Linux image | Windows image |
|---|---|---|---|
| `--config.file` | `/etc/imageadvisor/config.yml` | `/data/config.yml` | `C:\data\config.yml` |
| `--storage.path` | `/var/lib/imageadvisor` | `/data` | `C:\data` |
| `--docker.socket` | `/var/run/docker.sock`; Windows `\\.\pipe\docker_engine` | default | default |
| `--web.listen-address` | `:8080` (changes with 1.0) | default | default |
| `--log.level` | `info` (`debug`, `info`, `warn`, `error`) | default | default |
| `--log.format` | `logfmt` (`logfmt`, `json`) | default | default |
| `--version` | print the version and exit | | |

An unknown flag or an invalid value exits with code 2. `--storage.path` holds `state.json` (see
[State](#state)).

## Config file

Everything is optional; a missing file means the defaults. ImageAdvisor never writes the file.

```yaml
global:
  reconcile_interval: 60s          # read the containers; also right after Docker events
  check_interval: 24h              # registry check per image reference
  forget_after: 168h               # keep the state of removed containers this long

exclude:
  containers: [ "buildx_buildkit_*" ]   # container names, glob
  images: [ "ghcr.io/example/*" ]       # image references, glob (as written or normalized)

registries:                        # credentials; other registries are asked anonymously
  - host: ghcr.io
    username: ${GHCR_USER}
    password: ${GHCR_TOKEN}
  - host: docker.io                # Docker Hub
    username: ${HUB_USER:-}
    password: ${HUB_TOKEN:-}

container_labels:                  # Docker labels copied to the metrics (default: none)
  - com.example.team               # becomes container_label_com_example_team
  - { from: com.example.owner, to: owner_team }

labels:                            # static labels by rule
  - match: { stack: mailcow }
    labels: { team: mail }
  - match: { image: "ghcr.io/example/*" }
    labels: { own: "true" }
```

### Loading and reloading

- Unknown keys are an error.
- An invalid file at start: ImageAdvisor exits with code 1.
- The file is re-read when it changed (checked at every reconcile), on `SIGHUP` and on
  `POST /-/reload`. An invalid file then keeps the previous config and sets
  `imageadvisor_config_last_reload_successful` to 0; the log says why. A file that is missing or empty
  at reload counts as invalid.
- Invalid: a duration of 0 or less; a registry without `host`, or the same `host` twice; a label name
  that is not a valid Prometheus label name, starts with `__` or is one of ImageAdvisor's own labels
  (`container`, `image`, `reference`, `stack`, `state`, `mode`); two `container_labels` entries with the
  same target name; an unset environment variable without default; an unterminated `${` or an invalid
  variable name (the error names the key, never the value).

### Environment variables

In string values, `${VAR}` is replaced by the variable, `${VAR:-default}` by the variable or `default`
if it is unset or empty, and `$$` by `$`. An unset `${VAR}` without default makes the file invalid. Any
other `$` stays as it is.

### Intervals

- **`reconcile_interval`:** how often the containers are read and compared with the known registry
  digests. Docker events (container created, started, stopped, removed; image pulled, tagged, removed)
  trigger a reconcile at once, so this is only the fallback.
- **`check_interval`:** how often the registry is asked per image reference. A new reference is checked
  at once; after that the checks of all references are spread over the interval. Checks go out one at a
  time, at least 10 seconds apart per registry.
- A failed check is repeated after 5 minutes, doubling up to `check_interval`. A registry that answers
  429 (rate limit) is not asked for 5 minutes, doubling up to `check_interval`.

### Docker Hub pull limit

ImageAdvisor asks the registry through the Docker daemon (`GET /distribution/<reference>/json`). **For
Docker Hub, each check counts as one pull** against the pull limit of the host's IP or account. With
the default `check_interval` of 24h that is one pull per Hub image per day. Add Docker Hub credentials
under `registries` if the anonymous limit is tight; a registry mirror configured in the daemon is used
too.

### Registries

- `host` is the registry part of the image reference (`ghcr.io`, `registry.example.com:5000`);
  `docker.io` (or `index.docker.io`) is Docker Hub.
- An entry with a non-empty `password` sends the credentials with every check of that registry. An empty
  password (as `${HUB_TOKEN:-}` gives when the variable is unset) or no entry means anonymous.
- Credentials never appear in logs, metrics or the state file. Keep them out of the file with `${VAR}`.

### Excluding containers

A container is not reported at all if:

- its name matches `exclude.containers`;
- its image reference matches `exclude.images`, as written in the container (`nginx`) or normalized
  (`docker.io/library/nginx:latest`);
- it has the Docker label `io.github.motwok.imageadvisor.enable=false`.

In globs, `*` matches any characters, including `/`.

### Additional labels

The container series (`update_available`, its `since`, `pulled`) can carry additional labels, from
three sources; when two give the same name, the first one in this list wins:

1. **Rules** under `labels`: the keys of `match` (`stack`, `container`, `image`) are globs and all given
   keys must match; for each label the last matching rule in the file wins.
2. **Self-service:** a Docker label `io.github.motwok.imageadvisor.label.<name>=<value>` on the container
   becomes the label `<name>`.
3. **Allowlist** `container_labels`: the Docker label `from` is copied as `to`, or, without `to`, as
   `container_label_<from>` with every character other than letters, digits and `_` replaced by `_`.

Self-service and allowlist names that are invalid or own labels are skipped with a warning in the log.
Labels with an empty value are left out.

## State

`<--storage.path>/state.json` keeps, per image reference, the last registry digest and when the next
check is due, per container (for Swarm tasks per service and slot) the `since` time of an available
update, and the rate limit backoff per registry. So a restart neither loses `since` nor asks the
registries again. The file is written atomically after each change. An unreadable file or one written
by a newer version is renamed to `state.json.bak` and ImageAdvisor starts empty. If the directory is not
writable, ImageAdvisor keeps the state in memory and logs an error once per hour.

Entries of removed containers and references are dropped after `forget_after`; a container that comes
back before that continues where it left off.
