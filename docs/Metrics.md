# Metrics

All metrics have the prefix `imageadvisor_`; all are gauges except `check_errors_total`.

The metrics of the image check itself follow once it is implemented.

## ImageAdvisor itself

| Metric | Labels | Value |
|---|---|---|
| `check_duration_seconds` | | duration of the last check |
| `check_errors_total` | | counter of failed checks (Docker unreachable, ...) |
| `config_last_reload_successful` | | 0/1 |
| `build_info` | `version`, `revision`, `goversion` | 1 |
| `build_timestamp_seconds` | | build time of the binary |

## Example alerts

```yaml
groups:
  - name: imageadvisor
    rules:
      - alert: ImageAdvisorDown
        expr: up{job="imageadvisor"} == 0
        for: 5m
      - alert: ImageAdvisorCheckFailing
        expr: increase(imageadvisor_check_errors_total[1d]) > 0
```

Thresholds and windows are examples; choose your own.
