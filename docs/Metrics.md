# Metrics

All metrics have the prefix `imageadvisor_`; all are gauges except `reconcile_errors_total`.

## Containers

| Metric | Labels | Value | Present |
|---|---|---|---|
| `container_image_update_available` | `container`, `image`, `stack`, additional | 1: the registry has a newer image for the reference | `mode="checked"` |
| `container_image_update_available_since_timestamp_seconds` | same | when `update_available` went to 1 | while `update_available` is 1 |
| `container_image_update_pulled` | same | 1: the reference already points to another local image (pulled, container not recreated) | containers with a reference, not Swarm tasks |
| `container_image_info` | `container`, `reference`, `state`, `mode` | 1 | every reported container |

- `container`: the container name, the same value as cAdvisor's `name` label.
- `image`: the reference as written when the container was created (`nginx:1.27`). For Swarm tasks
  without the digest Swarm adds (`name:tag@sha256:...` shows as `name:tag`).
- `reference`: the normalized reference (`docker.io/library/nginx:1.27`); it joins `container_image_info`
  with the per-reference series below. No label for `mode="local"`.
- `stack`: the Compose project, for Swarm tasks the stack; no label without one.
- `state`: `running` (also paused and restarting) or `stopped` (created, exited, dead).
- Additional labels: see [Configuration.md](Configuration.md#additional-labels).

### Modes

| `mode` | Meaning |
|---|---|
| `checked` | a registry digest is known; `update_available` compares it with the running image |
| `pending` | not checked yet |
| `error` | the check failed and no registry digest is known; see `image_check_error` |
| `pinned` | the reference has a digest (`image@sha256:...`); nothing to check |
| `local` | the container was created from an image ID; nothing to check |

A failed check does not drop a known registry digest: the container stays `checked` and
`image_check_error` shows the failure.

## References

| Metric | Labels | Value | Present |
|---|---|---|---|
| `image_check_success` | `reference` | 1 if the last registry check succeeded, else 0 | after the first check |
| `image_check_error` | `reference`, `reason` | 1 | while the last check failed |
| `image_check_timestamp_seconds` | `reference` | time of the last successful check | after the first success |

`reason`:

| `reason` | Registry answer | Usual cause |
|---|---|---|
| `not_found` | 404 | locally built image, tag deleted |
| `denied` | 401, 403 | private image without credentials; Docker Hub also answers 403 for a repository that does not exist |
| `rate_limited` | 429 | pull limit reached |
| `unreachable` | anything else, no answer within 30 seconds | network, registry down |

These series exist only while a reported container uses the reference.

## ImageAdvisor itself

| Metric | Labels | Value |
|---|---|---|
| `reconcile_duration_seconds` | | duration of the last reconcile |
| `reconcile_errors_total` | | counter of failed reconciles (Docker unreachable, ...) |
| `config_last_reload_successful` | | 0/1 |
| `build_info` | `version`, `revision`, `goversion` | 1 |
| `build_timestamp_seconds` | | build time of the binary |

A scrape answers from the last results; it never triggers a Docker or registry request.

## Example alerts

```yaml
groups:
  - name: imageadvisor
    rules:
      - alert: ImageAdvisorDown
        expr: up{job="imageadvisor"} == 0
        for: 5m
      - alert: ImageUpdateNotRolledOut
        # Without the last part it also fires for containers stopped on purpose.
        expr: |
          (imageadvisor_container_image_update_available == 1
            and time() - imageadvisor_container_image_update_available_since_timestamp_seconds > 2 * 86400)
          and on(instance, container) imageadvisor_container_image_info{state="running"}
      - alert: ImagePulledNotRecreated
        expr: imageadvisor_container_image_update_pulled == 1
        for: 1h
      - alert: ImageCheckFailing
        # not_found and denied are mostly local images or missing credentials: exclude or configure them.
        expr: imageadvisor_image_check_error{reason=~"rate_limited|unreachable"} == 1
        for: 1d
      - alert: ImageCheckStale
        # With check_interval 24h.
        expr: time() - imageadvisor_image_check_timestamp_seconds > 3 * 86400
      - alert: ImageAdvisorReconcileFailing
        expr: increase(imageadvisor_reconcile_errors_total[1h]) > 0
```

Thresholds and windows are examples; choose your own.
