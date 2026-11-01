# Installing ImageAdvisor

1. Run the container.
2. Add the scrape job to Prometheus.

## Requirements

- Docker Engine (rootful) with API 1.41 or newer (Docker 20.10+), and one of:
  - Linux on `amd64` or `arm64`;
  - Windows Server 2022 or 2025 with Windows containers. A variant for Windows Server 2019 is built too
    but **not tested**, since there is no CI runner for it.
- Compose stacks and Swarm services are both supported. Not supported: rootless Docker, Podman.

ImageAdvisor only reads. It never pulls images, restarts containers or changes anything on the host.
It asks the registries through the Docker daemon, so the daemon's proxy, mirror and certificate
settings apply. **Every check of a Docker Hub image counts as one pull** against the Hub pull limit
(see [Configuration.md](Configuration.md#docker-hub-pull-limit)).

## 1. Run the container

### Linux

```yaml
services:
  imageadvisor:
    image: ghcr.io/motwok/imageadvisor:latest
    container_name: imageadvisor
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - imageadvisor-data:/data        # config.yml and state.json
    ports:
      - "8080:8080"                    # default port before 1.0, changes with 1.0
volumes:
  imageadvisor-data:
```

### Windows

The same image tag; Docker picks the variant for the host's Windows version. The named pipe is mounted
instead of the socket:

```yaml
services:
  imageadvisor:
    image: ghcr.io/motwok/imageadvisor:latest
    container_name: imageadvisor
    restart: unless-stopped
    volumes:
      - type: npipe
        source: \\.\pipe\docker_engine
        target: \\.\pipe\docker_engine
      - type: volume
        source: imageadvisor-data
        target: C:\data                # config.yml and state.json
    ports:
      - "8080:8080"                    # default port before 1.0, changes with 1.0
volumes:
  imageadvisor-data:
```

The image runs as `ContainerAdministrator`, because the Docker pipe is open to administrators only.

### Notes

- The image sets `--config.file` and `--storage.path` to the data volume. A `command:` replaces both, so
  repeat them when adding flags, for example on Linux:

  ```yaml
      command: [ "--config.file=/data/config.yml", "--storage.path=/data", "--log.level=debug" ]
  ```

- No capabilities, no `privileged` and no host paths besides the socket or pipe are needed.
- On hosts that also run cAdvisor, map a different host port.
- There is no `healthcheck:`: the image has no shell or `curl`. Prometheus sees the health through `up`
  and `/-/ready`.
- To leave a container out, give it the label `io.github.motwok.imageadvisor.enable=false` or use
  `exclude` in the config file.

## 2. Prometheus

```yaml
scrape_configs:
  - job_name: imageadvisor
    static_configs:
      - targets: [ "<host>:8080" ]
```

## 3. Check

```bash
curl -s http://<host>:8080/-/ready
curl -s http://<host>:8080/metrics | grep '^imageadvisor_container_image'
```

`/-/ready` answers 200 once the containers were read the first time; the registry results follow, one
check at a time (see [Configuration.md](Configuration.md#intervals)). Until then a container shows
`mode="pending"`.

## Update and rollback

- `:latest` gets every release; redeploy the stack to update. See [RELEASING.md](../RELEASING.md),
  including the automatic dependency releases.
- To stay on a version, use `ghcr.io/motwok/imageadvisor:X.Y.Z`. Version tags are never rebuilt.
- ImageAdvisor reports its own container like every other one, so a stuck update of ImageAdvisor
  itself shows up too.

## Uninstall

Remove the stack and the `imageadvisor-data` volume. ImageAdvisor leaves nothing else behind.
