# Installing ImageAdvisor

1. Run the container.
2. Add the scrape job to Prometheus.

## Requirements

- Linux, `amd64` or `arm64`.
- Docker Engine (rootful) with API 1.41 or newer (Docker 20.10+).

ImageAdvisor only reads. It never pulls images, restarts containers or changes anything on the host.

## 1. Run the container

```yaml
services:
  imageadvisor:
    image: ghcr.io/motwok/imageadvisor:latest
    container_name: imageadvisor
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - imageadvisor-data:/data        # config.yml
    ports:
      - "8080:8080"                    # default port before 1.0, changes with 1.0
volumes:
  imageadvisor-data:
```

- The image sets `--config.file=/data/config.yml`. A `command:` replaces it, so repeat it when adding
  flags, for example:

  ```yaml
      command: [ "--config.file=/data/config.yml", "--log.level=debug" ]
  ```

- On hosts that also run cAdvisor, map a different host port.
- There is no `healthcheck:`: the image has no shell or `curl`. Prometheus sees the health through `up`
  and `/-/ready`.

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
curl -s http://<host>:8080/metrics | grep '^imageadvisor_'
```

## Update and rollback

- `:latest` gets every release; redeploy the stack to update. See [RELEASING.md](../RELEASING.md),
  including the automatic dependency releases.
- To stay on a version, use `ghcr.io/motwok/imageadvisor:X.Y.Z`. Version tags are never rebuilt.

## Uninstall

Remove the stack and the `imageadvisor-data` volume. ImageAdvisor leaves nothing else behind.
