# Troubleshooting

Start with the container log (`docker logs imageadvisor`); `--log.level=debug` shows more.

## ImageAdvisor does not start or is not ready

- **Exit code 2:** an unknown flag or an invalid flag value; the log names it.
- **Exit code 1, "invalid config":** the config file has an error; the log names the line.
- **Exit code 1, "unsupported Docker":** the Docker API is older than 1.41 (Docker 20.10).
- **Not ready, "Docker not reachable":** the Docker socket is not mounted, or under another path than
  `--docker.socket`. `imageadvisor_check_errors_total` counts the attempts.

## Config changes have no effect

- The file is checked for changes before every check, so with a long `check_interval` send `SIGHUP`
  (`docker kill -s HUP imageadvisor`) or `POST /-/reload`.
- `imageadvisor_config_last_reload_successful 0` means the new file is invalid and the previous one is
  still active; the log says why.
