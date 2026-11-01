# Troubleshooting

Start with the container log (`docker logs imageadvisor`); `--log.level=debug` shows more.

## ImageAdvisor does not start or is not ready

- **Exit code 2:** an unknown flag or an invalid flag value; the log names it.
- **Exit code 1, "invalid config":** the config file has an error; the log names the line.
- **Exit code 1, "unsupported Docker":** the Docker API is older than 1.41 (Docker 20.10).
- **Not ready, "reconcile failed":** Docker is not reachable: the socket or pipe is not mounted, or
  under another path than `--docker.socket`. `imageadvisor_reconcile_errors_total` counts the attempts.
  On Windows, "Access is denied" means the container does not run as `ContainerAdministrator` (a
  `user:` in the stack overrides the image).

## A container is missing

- It is excluded: `exclude.containers`, `exclude.images` or the label
  `io.github.motwok.imageadvisor.enable=false`.
- It is an old Swarm task (not running) or is being removed; those are not reported.

## A container stays `pending` or shows `error`

- `pending`: not checked yet. Checks go out one at a time, at least 10 seconds apart per registry, and a
  registry that answered 429 is paused, so after a start with many images this takes a while.
- `error` with `imageadvisor_image_check_error`:
  - `not_found`: the image is not in the registry, usually a locally built image. Exclude it.
  - `denied`: a private image without credentials (add them under `registries`), or a Docker Hub
    repository that does not exist (also a locally built image).
  - `rate_limited`: the registry's pull limit; see
    [Configuration.md](Configuration.md#docker-hub-pull-limit).
  - `unreachable`: the daemon could not reach the registry.

  Each failed check logs a `registry check failed` line with the daemon's message.
- A failed check is retried after 5 minutes, doubling up to `check_interval`.

## `update_available` is 1, but the container is up to date

- The comparison is with the digest the registry gives for the reference the container was created
  with. If the container was created from another tag or an image ID, `image` and `mode` show it.
- `pulled` 1 means the new image is already on the host, but the container still runs the old one:
  recreate it (`docker compose up -d`, stack redeploy).

## Config changes have no effect

- The file is checked for changes at every reconcile (`reconcile_interval`); `SIGHUP`
  (`docker kill -s HUP imageadvisor`) or `POST /-/reload` reload at once.
- `imageadvisor_config_last_reload_successful 0` means the new file is invalid and the previous one is
  still active; the log says why.
