// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"maps"

	"github.com/motwok/ImageAdvisor/internal/metrics"
)

// BuildInfo describes the binary for imageadvisor_build_info.
type BuildInfo struct {
	Version, Revision, GoVersion string
	BuildTime                    int64
}

func m(name, help, typ string) metrics.Family {
	return metrics.Family{Name: "imageadvisor_" + name, Help: help, Type: typ}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Collect returns all metric families from the last reconcile and checks (spec 11).
func (c *Core) Collect(bi BuildInfo) []metrics.Family {
	c.mu.Lock()
	defer c.mu.Unlock()

	avail := m("container_image_update_available", "1 if the registry has a newer image for the container's reference.", "gauge")
	since := m("container_image_update_available_since_timestamp_seconds", "Time the update became available.", "gauge")
	pulled := m("container_image_update_pulled", "1 if the reference points to another local image than the container runs.", "gauge")
	info := m("container_image_info", "Changing properties of a reported container.", "gauge")
	success := m("image_check_success", "1 if the last registry check of the reference succeeded.", "gauge")
	checkErr := m("image_check_error", "1 while the last registry check of the reference failed.", "gauge")
	stamp := m("image_check_timestamp_seconds", "Time of the last successful registry check.", "gauge")

	refs := map[string]bool{}
	for _, e := range c.view {
		l := map[string]string{}
		maps.Copy(l, e.Labels)
		l["container"], l["image"], l["stack"] = e.Container, e.Image, e.Stack
		if e.Available >= 0 {
			avail.Samples = append(avail.Samples, metrics.Sample{Labels: l, Value: float64(e.Available)})
		}
		if e.Available == 1 {
			since.Samples = append(since.Samples, metrics.Sample{Labels: l, Value: float64(e.Since)})
		}
		if e.Pulled >= 0 {
			pulled.Samples = append(pulled.Samples, metrics.Sample{Labels: l, Value: float64(e.Pulled)})
		}
		info.Samples = append(info.Samples, metrics.Sample{Labels: map[string]string{
			"container": e.Container, "reference": e.Ref, "state": e.State, "mode": e.Mode}, Value: 1})
		if e.Mode == "pending" || e.Mode == "error" || e.Mode == "checked" {
			refs[e.Ref] = true
		}
	}
	for r := range refs {
		rs := c.st.Refs[r]
		if rs == nil || !rs.Checked {
			continue
		}
		l := map[string]string{"reference": r}
		success.Samples = append(success.Samples, metrics.Sample{Labels: l, Value: b2f(rs.Reason == "")})
		if rs.Reason != "" {
			checkErr.Samples = append(checkErr.Samples, metrics.Sample{
				Labels: map[string]string{"reference": r, "reason": rs.Reason}, Value: 1})
		}
		if rs.LastSuccess > 0 {
			stamp.Samples = append(stamp.Samples, metrics.Sample{Labels: l, Value: float64(rs.LastSuccess)})
		}
	}

	one := func(f metrics.Family, val float64) metrics.Family {
		f.Samples = []metrics.Sample{{Value: val}}
		return f
	}
	return []metrics.Family{
		avail, since, pulled, info, success, checkErr, stamp,
		one(m("reconcile_duration_seconds", "Duration of the last reconcile.", "gauge"), c.recSeconds),
		one(m("reconcile_errors_total", "Failed reconciles.", "counter"), c.recErrors),
		one(m("config_last_reload_successful", "1 if the last config load succeeded.", "gauge"), b2f(c.reloadOK)),
		{Name: "imageadvisor_build_info", Help: "Build information.", Type: "gauge", Samples: []metrics.Sample{{Labels: map[string]string{
			"version": bi.Version, "revision": bi.Revision, "goversion": bi.GoVersion}, Value: 1}}},
		one(m("build_timestamp_seconds", "Build time of the binary.", "gauge"), float64(bi.BuildTime)),
	}
}
