package doctor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Severity orders findings from worst to mildest.
type Severity string

const (
	Fail Severity = "fail"
	Warn Severity = "warn"
	Info Severity = "info"
)

// Finding is one problem, with the reason and the fix.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Why      string   `json:"why"`
	Fix      []string `json:"fix,omitempty"`
	Docs     string   `json:"docs,omitempty"`
}

// Check looks at the facts. ran is false when the check did not apply, for
// example because the daemon could not be reached.
type Check struct {
	ID   string
	Name string
	Run  func(f *Facts) (findings []Finding, ran bool)
}

// Checks lists every check in the order they are reported.
var Checks = []Check{
	{"docker-cli", "Docker CLI installed", checkCLI},
	{"daemon", "Docker daemon reachable", checkDaemon},
	{"snap", "Docker not installed as a snap", checkSnap},
	{"compose", "Compose v2 installed", checkCompose},
	{"disk", "Free space for Docker", checkDisk},
	{"restarts", "No containers in a restart loop", checkRestarts},
	{"health", "No unhealthy containers", checkHealth},
	{"ufw", "Published ports not hidden from UFW", checkUFW},
	{"compose-file", "Compose file valid", checkComposeFile},
	{"compose-vars", "Compose variables set", checkComposeVars},
	{"host-gateway", "host.docker.internal resolvable", checkHostGateway},
	{"ports", "Published ports free", checkPorts},
	{"bind-mounts", "Bind mount sources usable", checkBindMounts},
	{"rootless-ports", "Rootless Docker can bind its ports", checkRootlessPorts},
}

// Result is the outcome of running every check.
type Result struct {
	Findings []Finding `json:"findings"`
	Passed   []string  `json:"passed"`
	Skipped  []string  `json:"skipped"`
}

// Run runs every check against f.
func Run(f *Facts) Result {
	var r Result
	for _, c := range Checks {
		findings, ran := c.Run(f)
		switch {
		case !ran:
			r.Skipped = append(r.Skipped, c.Name)
		case len(findings) == 0:
			r.Passed = append(r.Passed, c.Name)
		default:
			for i := range findings {
				findings[i].Check = c.ID
			}
			r.Findings = append(r.Findings, findings...)
		}
	}
	rank := map[Severity]int{Fail: 0, Warn: 1, Info: 2}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		return rank[r.Findings[i].Severity] < rank[r.Findings[j].Severity]
	})
	return r
}

func (f *Facts) daemonUp() bool { return f.DockerPath != "" && f.Info != nil }

func checkCLI(f *Facts) ([]Finding, bool) {
	if f.DockerPath != "" {
		return nil, true
	}
	return []Finding{{
		Severity: Fail,
		Title:    "The docker command is not installed",
		Why:      "Nothing else can be checked until the Docker CLI is on your PATH.",
		Fix:      installFix(f.GOOS),
		Docs:     "https://docs.docker.com/engine/install/",
	}}, true
}

func installFix(goos string) []string {
	if goos == "linux" {
		return []string{"curl -fsSL https://get.docker.com | sudo sh"}
	}
	return []string{"Install Docker Desktop, OrbStack or Colima, then start it."}
}

func checkDaemon(f *Facts) ([]Finding, bool) {
	if f.DockerPath == "" {
		return nil, false
	}
	if f.Info != nil {
		return nil, true
	}
	msg := strings.ToLower(f.DaemonErr)
	switch {
	case strings.Contains(msg, "permission denied"):
		return []Finding{{
			Severity: Fail,
			Title:    "Your user isn't allowed to use the Docker socket",
			Why:      "The daemon is running, but /var/run/docker.sock only accepts root and the docker group, and your user is in neither. Being in the docker group is equivalent to root on this machine.",
			Fix: []string{
				"sudo usermod -aG docker $USER",
				"newgrp docker    # or log out and back in",
			},
			Docs: "https://docs.docker.com/engine/install/linux-postinstall/",
		}}, true
	case strings.Contains(msg, "is the docker daemon running"),
		strings.Contains(msg, "no such file or directory"),
		strings.Contains(msg, "cannot connect"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "failed to connect"):
		fix := []string{"sudo systemctl enable --now docker"}
		if f.GOOS != "linux" {
			fix = []string{"Start Docker Desktop (or run `colima start` / open OrbStack)."}
		}
		return []Finding{{
			Severity: Fail,
			Title:    "The Docker daemon isn't running",
			Why:      "The docker command is installed, but nothing answers on its socket:\n" + firstLine(f.DaemonErr),
			Fix:      fix,
		}}, true
	default:
		return []Finding{{
			Severity: Fail,
			Title:    "The Docker daemon didn't answer",
			Why:      firstLine(f.DaemonErr),
			Fix:      []string{"docker info"},
		}}, true
	}
}

func (f *Facts) snapDocker() bool {
	if f.GOOS != "linux" {
		return false
	}
	if strings.HasPrefix(f.DockerPath, "/snap/") {
		return true
	}
	return f.Info != nil && strings.HasPrefix(f.Info.DockerRootDir, "/var/snap/docker")
}

func checkSnap(f *Facts) ([]Finding, bool) {
	if f.GOOS != "linux" || f.DockerPath == "" {
		return nil, false
	}
	if !f.snapDocker() {
		return nil, true
	}
	return []Finding{{
		Severity: Warn,
		Title:    "Docker is installed as a snap",
		Why: "The snap is confined: the daemon can only reach your home directory (and /media or /mnt when removable-media is connected). " +
			"Bind mounts anywhere else, such as /opt or /srv, fail with `mkdir ...: read-only file system` even though the folder exists. " +
			"Most self-hosting guides assume Docker from Docker's own packages.",
		Fix: []string{
			"# removes the snap's images, containers and volumes",
			"sudo snap remove --purge docker",
			"curl -fsSL https://get.docker.com | sudo sh",
		},
		Docs: "https://docs.docker.com/engine/install/ubuntu/",
	}}, true
}

func checkCompose(f *Facts) ([]Finding, bool) {
	if f.DockerPath == "" {
		return nil, false
	}
	if f.ComposeVersion != "" {
		return nil, true
	}
	if f.ComposeV1 {
		return []Finding{{
			Severity: Warn,
			Title:    "Only the old docker-compose v1 is installed",
			Why:      "Compose v1 stopped getting updates in 2023. Current Compose files use features it doesn't understand, and most guides now use `docker compose` with a space.",
			Fix:      composeFix(f.GOOS),
			Docs:     "https://docs.docker.com/compose/releases/migrate/",
		}}, true
	}
	return []Finding{{
		Severity: Info,
		Title:    "Docker Compose isn't installed",
		Why:      "`docker compose` isn't available, which most self-hosted apps use to start.",
		Fix:      composeFix(f.GOOS),
		Docs:     "https://docs.docker.com/compose/install/linux/",
	}}, true
}

func composeFix(goos string) []string {
	if goos == "linux" {
		return []string{
			"sudo apt-get install docker-compose-plugin    # Debian/Ubuntu with Docker's apt repo",
			"sudo dnf install docker-compose-plugin        # Fedora/RHEL with Docker's repo",
		}
	}
	return []string{"Update Docker Desktop; it ships Compose v2."}
}

const gib = 1 << 30

func checkDisk(f *Facts) ([]Finding, bool) {
	if !f.daemonUp() || f.DiskTotal == 0 {
		return nil, false
	}
	pct := float64(f.DiskFree) / float64(f.DiskTotal) * 100
	if f.DiskFree >= 5*gib && pct >= 10 {
		return nil, true
	}
	return []Finding{{
		Severity: Warn,
		Title:    fmt.Sprintf("Low disk space for Docker: %.1f GiB free (%.0f%%)", float64(f.DiskFree)/gib, pct),
		Why:      "Docker keeps images, containers and volumes in " + f.Info.DockerRootDir + ". When it fills up, pulls fail, databases crash and containers can't write their logs.",
		Fix: []string{
			"docker system df                 # see what uses the space",
			"docker image prune -a            # remove images no container uses",
			"docker builder prune             # remove the build cache",
		},
		Docs: "https://docs.docker.com/engine/manage-resources/pruning/",
	}}, true
}

func checkRestarts(f *Facts) ([]Finding, bool) {
	if !f.daemonUp() {
		return nil, false
	}
	var findings []Finding
	for _, c := range f.Containers {
		name := c.ShortName()
		switch {
		case c.State.OOMKilled:
			findings = append(findings, Finding{
				Severity: Fail,
				Title:    fmt.Sprintf("%s was killed for running out of memory", name),
				Why:      "The kernel stopped it because it used more memory than it was allowed (OOMKilled). It will keep dying the same way under load.",
				Fix: []string{
					"docker stats --no-stream " + name,
					"# raise or remove its memory limit (mem_limit / deploy.resources.limits.memory), or add RAM or swap",
				},
				Docs: "https://docs.docker.com/engine/containers/resource_constraints/",
			})
		case c.State.Restarting || (c.RestartCount >= 3 && !c.State.Running):
			why := fmt.Sprintf("It has restarted %d times and isn't staying up. Exit code %d", c.RestartCount, c.State.ExitCode)
			if hint := exitHint(c.State.ExitCode); hint != "" {
				why += ": " + hint
			}
			findings = append(findings, Finding{
				Severity: Fail,
				Title:    fmt.Sprintf("%s is in a restart loop", name),
				Why:      why + ".",
				Fix:      []string{"docker logs --tail 50 " + name},
			})
		}
	}
	return findings, true
}

func exitHint(code int) string {
	switch code {
	case 1:
		return "the app itself exited with an error, the reason is in its logs"
	case 125:
		return "Docker couldn't create the container"
	case 126:
		return "the entrypoint exists but isn't executable"
	case 127:
		return "the entrypoint or command wasn't found in the image"
	case 137:
		return "it was killed (SIGKILL), often for memory"
	case 139:
		return "it crashed with a segmentation fault, often the wrong CPU architecture for the image"
	case 143:
		return "it was asked to stop (SIGTERM)"
	}
	return ""
}

func checkHealth(f *Facts) ([]Finding, bool) {
	if !f.daemonUp() {
		return nil, false
	}
	var findings []Finding
	for _, c := range f.Containers {
		h := c.State.Health
		if h == nil || h.Status != "unhealthy" {
			continue
		}
		why := "Its healthcheck keeps failing, so containers that depend on it with `condition: service_healthy` won't start."
		if n := len(h.Log); n > 0 {
			if out := strings.TrimSpace(h.Log[n-1].Output); out != "" {
				why += "\nLast check said: " + truncate(firstLine(out), 200)
			}
		}
		findings = append(findings, Finding{
			Severity: Warn,
			Title:    c.ShortName() + " is unhealthy",
			Why:      why,
			Fix: []string{
				"docker inspect --format '{{json .State.Health}}' " + c.ShortName(),
				"docker logs --tail 50 " + c.ShortName(),
			},
		})
	}
	return findings, true
}

func checkUFW(f *Facts) ([]Finding, bool) {
	if f.GOOS != "linux" || !f.daemonUp() || !f.UFWEnabled || f.Info.Rootless() {
		return nil, false
	}
	var exposed []string
	for _, c := range f.Containers {
		if !c.State.Running {
			continue
		}
		for port, bindings := range c.NetworkSettings.Ports {
			for _, b := range bindings {
				if b.HostPort == "" || !anyAddress(b.HostIP) {
					continue
				}
				exposed = append(exposed, fmt.Sprintf("%s → %s (%s)", b.HostPort, c.ShortName(), port))
				break
			}
		}
	}
	if len(exposed) == 0 {
		return nil, true
	}
	sort.Strings(exposed)
	return []Finding{{
		Severity: Warn,
		Title:    "UFW is on, but it doesn't block these published ports",
		Why: "Docker adds its own iptables rules for published ports, and they're checked before UFW's. A `ufw deny` doesn't stop traffic to:\n" +
			strings.Join(dedupe(exposed), "\n"),
		Fix: []string{
			`# only reached through a reverse proxy or from this machine? bind to localhost:`,
			`ports: ["127.0.0.1:8080:80"]`,
			`# otherwise add rules to the DOCKER-USER chain, which Docker leaves alone`,
		},
		Docs: "https://docs.docker.com/engine/network/packet-filtering-firewalls/#docker-and-ufw",
	}}, true
}

func anyAddress(ip string) bool {
	return ip == "" || ip == "0.0.0.0" || ip == "::"
}

func checkComposeFile(f *Facts) ([]Finding, bool) {
	// Without Compose v2 the file was never read; the compose check says why.
	if len(f.ComposeFiles) == 0 || f.composeCmd == nil {
		return nil, false
	}
	if f.ComposeErr == "" {
		return nil, true
	}
	return []Finding{{
		Severity: Fail,
		Title:    "Compose can't read " + strings.Join(baseNames(f.ComposeFiles), ", "),
		Why:      f.ComposeErr,
		Fix:      []string{"docker compose config"},
	}}, true
}

func checkComposeVars(f *Facts) ([]Finding, bool) {
	if len(f.ComposeFiles) == 0 || f.composeCmd == nil {
		return nil, false
	}
	if len(f.ComposeWarnings) == 0 {
		return nil, true
	}
	return []Finding{{
		Severity: Warn,
		Title:    "The Compose file uses variables that aren't set: " + strings.Join(f.ComposeWarnings, ", "),
		Why:      "Compose replaces them with an empty string, so passwords, paths or image tags end up blank. That breaks things quietly.",
		Fix: []string{
			"# put them in a .env file next to the Compose file:",
			f.ComposeWarnings[0] + "=...",
		},
		Docs: "https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/",
	}}, true
}

// definesHostGateway reports whether the daemon adds host.docker.internal by
// itself, as Docker Desktop (also on Linux) and OrbStack do.
func (f *Facts) definesHostGateway() bool {
	if f.Info == nil {
		return false
	}
	os := strings.ToLower(f.Info.OperatingSystem)
	return strings.Contains(os, "docker desktop") || strings.Contains(os, "orbstack")
}

func checkHostGateway(f *Facts) ([]Finding, bool) {
	if f.Project == nil || f.GOOS != "linux" || f.definesHostGateway() {
		return nil, false
	}
	var missing []string
	for name, svc := range f.Project.Services {
		if svc.mentions("host.docker.internal") && !strings.Contains(string(svc.ExtraHosts), "host.docker.internal") {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil, true
	}
	sort.Strings(missing)
	return []Finding{{
		Severity: Fail,
		Title:    "host.docker.internal won't resolve in: " + strings.Join(missing, ", "),
		Why: "Docker Desktop defines host.docker.internal, Docker Engine on Linux doesn't. Add it to each service that uses it. " +
			"If the app then refuses to call it as a private address (Appwrite 2.4+, for example), it needs its own allowlist setting as well.",
		Fix: []string{
			"extra_hosts:",
			`  - "host.docker.internal:host-gateway"`,
		},
		Docs: "https://docs.docker.com/reference/cli/docker/container/run/#add-host",
	}}, true
}

func checkPorts(f *Facts) ([]Finding, bool) {
	if f.Project == nil || f.PortInUse == nil {
		return nil, false
	}
	// Ports this project's own running containers hold are expected to be busy.
	own := map[string]bool{}
	for _, c := range f.Containers {
		if !c.State.Running || c.Config.Labels["com.docker.compose.project"] != f.Project.Name {
			continue
		}
		for port, bindings := range c.NetworkSettings.Ports {
			proto := "tcp"
			if strings.HasSuffix(port, "/udp") {
				proto = "udp"
			}
			for _, b := range bindings {
				own[b.HostPort+"/"+proto] = true
			}
		}
	}
	var findings []Finding
	seen := map[string]bool{}
	for _, name := range sortedServices(f.Project) {
		for _, p := range f.Project.Services[name].Ports {
			for _, hp := range p.HostPorts() {
				key := fmt.Sprintf("%d/%s", hp, p.proto())
				if seen[key] || own[key] {
					continue
				}
				seen[key] = true
				if !f.PortInUse(hp, p.proto()) {
					continue
				}
				findings = append(findings, Finding{
					Severity: Fail,
					Title:    fmt.Sprintf("Port %d is already in use (needed by %s)", hp, name),
					Why:      "Something else on this machine is listening on it, so the container fails with `bind: address already in use`.",
					Fix:      portFix(f.GOOS, hp, p.proto()),
				})
			}
		}
	}
	return findings, true
}

func portFix(goos string, port int, proto string) []string {
	if goos == "linux" {
		flag := "-ltnp"
		if proto == "udp" {
			flag = "-lunp"
		}
		return []string{
			fmt.Sprintf("sudo ss %s 'sport = :%d'    # see what holds it", flag, port),
			"# stop that program, or publish the service on another host port",
		}
	}
	return []string{
		fmt.Sprintf("lsof -nP -i%s:%d    # see what holds it", strings.ToUpper(proto), port),
		"# stop that program, or publish the service on another host port",
	}
}

func checkBindMounts(f *Facts) ([]Finding, bool) {
	if f.Project == nil || f.Exists == nil {
		return nil, false
	}
	var findings []Finding
	for _, name := range sortedServices(f.Project) {
		for _, v := range f.Project.Services[name].Volumes {
			if v.Type != "bind" || v.Source == "" {
				continue
			}
			shown := f.shortPath(v.Source)
			if f.snapDocker() && !f.snapCanReach(v.Source) {
				findings = append(findings, Finding{
					Severity: Fail,
					Title:    fmt.Sprintf("Snap Docker can't use %s (%s)", shown, name),
					Why:      "Docker is installed as a snap, which can only reach your home directory. This mount fails with `read-only file system`.",
					Fix: []string{
						"sudo snap remove --purge docker",
						"curl -fsSL https://get.docker.com | sudo sh",
					},
					Docs: "https://docs.docker.com/engine/install/ubuntu/",
				})
				continue
			}
			if f.Exists(v.Source) {
				continue
			}
			if looksLikeFile(v.Source) {
				findings = append(findings, Finding{
					Severity: Fail,
					Title:    fmt.Sprintf("%s doesn't exist, so Docker will create it as a directory (%s)", shown, name),
					Why:      "When a bind mount source is missing, Docker creates an empty directory there. The app then finds a folder where it expects a file and fails with `is a directory` or `not a directory`.",
					Fix: []string{
						"# remove the directory Docker created (if any), then create the real file:",
						"sudo rmdir " + shown + " 2>/dev/null; touch " + shown,
					},
				})
				continue
			}
			if v.Bind != nil && !v.Bind.CreateHostPath {
				findings = append(findings, Finding{
					Severity: Fail,
					Title:    fmt.Sprintf("%s doesn't exist (%s)", shown, name),
					Why:      "This mount sets create_host_path: false, so the container won't start until the folder exists.",
					Fix:      []string{"mkdir -p " + shown},
				})
				continue
			}
			findings = append(findings, Finding{
				Severity: Info,
				Title:    fmt.Sprintf("%s doesn't exist yet (%s)", shown, name),
				Why:      "Docker will create it owned by root. Apps that run as a normal user inside the container then can't write to it (`permission denied`).",
				Fix:      []string{"mkdir -p " + shown + "    # create it yourself, owned by you"},
			})
		}
	}
	return findings, true
}

func (f *Facts) snapCanReach(path string) bool {
	for _, root := range []string{f.Home, "/media", "/mnt"} {
		if root != "" && (path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")) {
			return true
		}
	}
	return false
}

var fileExts = map[string]bool{
	".yml": true, ".yaml": true, ".json": true, ".toml": true, ".conf": true, ".cfg": true,
	".ini": true, ".env": true, ".xml": true, ".js": true, ".py": true, ".sh": true,
	".sql": true, ".pem": true, ".crt": true, ".key": true, ".txt": true, ".db": true, ".sqlite": true,
}

func looksLikeFile(path string) bool {
	return fileExts[strings.ToLower(filepath.Ext(path))]
}

func checkRootlessPorts(f *Facts) ([]Finding, bool) {
	if f.Project == nil || f.Info == nil || !f.Info.Rootless() || f.GOOS != "linux" {
		return nil, false
	}
	limit := f.UnprivPortStart
	if limit == 0 {
		limit = 1024
	}
	var low []string
	for _, name := range sortedServices(f.Project) {
		for _, p := range f.Project.Services[name].Ports {
			for _, hp := range p.HostPorts() {
				if hp < limit {
					low = append(low, fmt.Sprintf("%d (%s)", hp, name))
				}
			}
		}
	}
	if len(low) == 0 {
		return nil, true
	}
	return []Finding{{
		Severity: Fail,
		Title:    "Rootless Docker can't publish ports below " + fmt.Sprint(limit) + ": " + strings.Join(dedupe(low), ", "),
		Why:      "Rootless Docker runs as your user, and Linux only lets root bind ports below net.ipv4.ip_unprivileged_port_start.",
		Fix: []string{
			fmt.Sprintf("echo 'net.ipv4.ip_unprivileged_port_start=%d' | sudo tee /etc/sysctl.d/99-rootless-docker.conf", lowestPort(low)),
			"sudo sysctl --system",
		},
		Docs: "https://docs.docker.com/engine/security/rootless/#exposing-privileged-ports",
	}}, true
}

func lowestPort(entries []string) int {
	lowest := 1024
	for _, e := range entries {
		var p int
		if _, err := fmt.Sscanf(e, "%d", &p); err == nil && p < lowest {
			lowest = p
		}
	}
	return lowest
}

func sortedServices(p *Project) []string {
	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}

// shortPath shows paths under the working directory as ./relative.
func (f *Facts) shortPath(p string) string {
	if f.Dir == "" {
		return p
	}
	rel, err := filepath.Rel(f.Dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return p
	}
	return "./" + rel
}
