package doctor

import (
	"fmt"
	"sort"
	"strings"
)

// unboundedLogs reports whether the container keeps every log line forever:
// json-file without max-size. The local driver rotates by default, and
// drivers such as journald or syslog hand the logs to something else.
func (c Container) unboundedLogs() bool {
	lc := c.HostConfig.LogConfig
	return lc.Type == "json-file" && lc.Config["max-size"] == ""
}

func (f *Facts) unboundedLogContainers() []Container {
	var out []Container
	for _, c := range f.Containers {
		if c.unboundedLogs() {
			out = append(out, c)
		}
	}
	return out
}

func (f *Facts) diskLow() bool {
	if f.DiskTotal == 0 {
		return false
	}
	return f.DiskFree < 5*gib || float64(f.DiskFree)/float64(f.DiskTotal) < 0.10
}

func checkLogs(f *Facts) ([]Finding, bool) {
	if !f.daemonUp() {
		return nil, false
	}
	unbounded := f.unboundedLogContainers()
	if len(unbounded) == 0 {
		return nil, true
	}

	names := make([]string, 0, len(unbounded))
	var sized []Container
	for _, c := range unbounded {
		names = append(names, c.ShortName())
		if _, ok := f.LogSize[c.ShortName()]; ok {
			sized = append(sized, c)
		}
	}
	sort.Strings(names)
	list := strings.Join(names, ", ")
	if len(names) > 5 {
		list = strings.Join(names[:5], ", ") + fmt.Sprintf(" and %d more", len(names)-5)
	}

	why := "They use the json-file driver without max-size, which keeps every line forever. " +
		"One chatty container can fill the disk over a few months."
	severity := Info
	if len(sized) > 0 {
		sort.Slice(sized, func(i, j int) bool {
			return f.LogSize[sized[i].ShortName()] > f.LogSize[sized[j].ShortName()]
		})
		var biggest []string
		for _, c := range sized[:min(3, len(sized))] {
			size := f.LogSize[c.ShortName()]
			biggest = append(biggest, fmt.Sprintf("%s %s", c.ShortName(), humanBytes(size)))
			if size >= gib {
				severity = Warn
			}
		}
		why += "\nLargest logs: " + strings.Join(biggest, ", ")
	}
	if f.diskLow() {
		severity = Warn
		why += "\nThe disk is already low on space, and these logs keep growing."
	}

	where := "# in /etc/docker/daemon.json, then restart Docker:"
	if f.definesHostGateway() {
		where = "# in Docker Desktop: Settings → Docker Engine, then Apply & restart:"
	}
	return []Finding{{
		Severity: severity,
		Title:    fmt.Sprintf("%s logs with no size limit: %s", pluralVerb(len(unbounded), "container keeps", "containers keep"), list),
		Why:      why,
		Fix: []string{
			where,
			`{ "log-driver": "local" }`,
			"# it applies to containers created afterwards: docker compose up -d --force-recreate",
			"# or per service in compose.yaml:",
			"logging:",
			"  driver: local",
		},
		Docs: "https://docs.docker.com/engine/logging/drivers/local/",
	}}, true
}

func humanBytes(n int64) string {
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
