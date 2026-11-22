package doctor

import (
	"encoding/json"
	"strings"
	"testing"
)

// linuxFacts is a healthy Linux machine with the daemon up.
func linuxFacts() *Facts {
	return &Facts{
		GOOS:           "linux",
		Home:           "/home/ada",
		DockerPath:     "/usr/bin/docker",
		Info:           &DockerInfo{ServerVersion: "27.3.1", DockerRootDir: "/var/lib/docker"},
		ComposeVersion: "2.29.7",
		composeCmd:     []string{"docker", "compose"},
		DiskFree:       50 * gib,
		DiskTotal:      100 * gib,
		PortInUse:      func(int, string) bool { return false },
		Exists:         func(string) bool { return true },
	}
}

func container(t *testing.T, js string) Container {
	t.Helper()
	var c Container
	if err := json.Unmarshal([]byte(js), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func project(t *testing.T, js string) *Project {
	t.Helper()
	p, err := ParseProject([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// findings runs one check and fails the test if it didn't run.
func findings(t *testing.T, id string, f *Facts) []Finding {
	t.Helper()
	for _, c := range Checks {
		if c.ID == id {
			got, ran := c.Run(f)
			if !ran {
				t.Fatalf("check %s did not run", id)
			}
			return got
		}
	}
	t.Fatalf("no check %s", id)
	return nil
}

func wantOne(t *testing.T, got []Finding, sev Severity, titleHas string) Finding {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	if got[0].Severity != sev || !strings.Contains(got[0].Title, titleHas) {
		t.Fatalf("finding = %s %q, want %s containing %q", got[0].Severity, got[0].Title, sev, titleHas)
	}
	return got[0]
}

func TestHealthyMachineHasNoFindings(t *testing.T) {
	f := linuxFacts()
	f.Project = project(t, `{"name":"app","services":{"web":{"ports":[{"target":80,"published":"8080"}]}}}`)
	f.ComposeFiles = []string{"/home/ada/app/compose.yaml"}
	r := Run(f)
	if len(r.Findings) != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if len(r.Passed) == 0 {
		t.Fatal("nothing passed")
	}
}

func TestDockerMissing(t *testing.T) {
	r := Run(&Facts{GOOS: "linux"})
	if len(r.Findings) != 1 || r.Findings[0].Check != "docker-cli" || !r.HasFailures() {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestDaemonErrors(t *testing.T) {
	cases := []struct{ err, title string }{
		{"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock", "isn't allowed"},
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", "isn't running"},
		{"error during connect: something odd", "didn't answer"},
	}
	for _, c := range cases {
		f := linuxFacts()
		f.Info, f.DaemonErr = nil, c.err
		wantOne(t, findings(t, "daemon", f), Fail, c.title)
	}
}

func TestSnapDocker(t *testing.T) {
	f := linuxFacts()
	f.DockerPath = "/snap/docker/2915/bin/docker"
	wantOne(t, findings(t, "snap", f), Warn, "snap")

	f = linuxFacts()
	f.Info.DockerRootDir = "/var/snap/docker/common/var-lib-docker"
	wantOne(t, findings(t, "snap", f), Warn, "snap")

	f = linuxFacts()
	f.GOOS = "darwin"
	if _, ran := checkSnap(f); ran {
		t.Error("snap check ran on macOS")
	}
}

func TestComposeVersions(t *testing.T) {
	f := linuxFacts()
	f.ComposeVersion, f.ComposeV1 = "", true
	wantOne(t, findings(t, "compose", f), Warn, "v1")

	f.ComposeV1 = false
	wantOne(t, findings(t, "compose", f), Info, "isn't installed")
}

func TestLowDisk(t *testing.T) {
	f := linuxFacts()
	f.DiskFree, f.DiskTotal = 2*gib, 100*gib
	wantOne(t, findings(t, "disk", f), Warn, "2.0 GiB free")

	// Plenty of GiB but under 10% still warns.
	f.DiskFree, f.DiskTotal = 40*gib, 1000*gib
	wantOne(t, findings(t, "disk", f), Warn, "4%")
}

func TestRestartLoopAndOOM(t *testing.T) {
	f := linuxFacts()
	f.Containers = []Container{
		container(t, `{"Name":"/ok","State":{"Status":"running","Running":true},"RestartCount":5}`),
		container(t, `{"Name":"/loop","State":{"Status":"restarting","Restarting":true,"ExitCode":127},"RestartCount":12}`),
		container(t, `{"Name":"/big","State":{"Status":"exited","OOMKilled":true,"ExitCode":137}}`),
		container(t, `{"Name":"/oneshot","State":{"Status":"exited","ExitCode":0}}`),
	}
	got := findings(t, "restarts", f)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "loop is in a restart loop") || !strings.Contains(got[0].Why, "wasn't found") {
		t.Errorf("restart finding = %+v", got[0])
	}
	if !strings.Contains(got[1].Title, "big was killed") {
		t.Errorf("OOM finding = %+v", got[1])
	}
}

func TestUnhealthy(t *testing.T) {
	f := linuxFacts()
	f.Containers = []Container{
		container(t, `{"Name":"/db","State":{"Status":"running","Running":true,"Health":{"Status":"unhealthy","Log":[{"ExitCode":1,"Output":"pg_isready: no response\n"}]}}}`),
		container(t, `{"Name":"/web","State":{"Status":"running","Running":true,"Health":{"Status":"healthy"}}}`),
	}
	fd := wantOne(t, findings(t, "health", f), Warn, "db is unhealthy")
	if !strings.Contains(fd.Why, "pg_isready: no response") {
		t.Errorf("Why = %q, want the last health output", fd.Why)
	}
}

func TestUFWBypass(t *testing.T) {
	f := linuxFacts()
	f.UFWEnabled = true
	f.Containers = []Container{
		container(t, `{"Name":"/web","State":{"Running":true},"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"8080"},{"HostIp":"::","HostPort":"8080"}]}}}`),
		container(t, `{"Name":"/admin","State":{"Running":true},"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"9000"}]}}}`),
		container(t, `{"Name":"/internal","State":{"Running":true},"NetworkSettings":{"Ports":{"5432/tcp":null}}}`),
	}
	fd := wantOne(t, findings(t, "ufw", f), Warn, "UFW")
	if !strings.Contains(fd.Why, "8080 → web") || strings.Contains(fd.Why, "9000") {
		t.Errorf("Why = %q", fd.Why)
	}

	f.UFWEnabled = false
	if _, ran := checkUFW(f); ran {
		t.Error("ufw check ran with UFW off")
	}
}

func TestComposeFileError(t *testing.T) {
	f := linuxFacts()
	f.ComposeFiles = []string{"/home/ada/app/compose.yaml"}
	f.ComposeErr = "services.web.ports must be a list"
	wantOne(t, findings(t, "compose-file", f), Fail, "compose.yaml")
}

func TestComposeVars(t *testing.T) {
	f := linuxFacts()
	f.ComposeFiles = []string{"/home/ada/app/compose.yaml"}
	f.ComposeWarnings = []string{"DB_PASSWORD", "TZ"}
	fd := wantOne(t, findings(t, "compose-vars", f), Warn, "DB_PASSWORD, TZ")
	if fd.Fix[1] != "DB_PASSWORD=..." {
		t.Errorf("Fix = %v", fd.Fix)
	}
}

func TestHostGateway(t *testing.T) {
	f := linuxFacts()
	f.Project = project(t, `{"name":"app","services":{
		"web":    {"environment":{"API":"http://host.docker.internal:3000"}},
		"fixed":  {"environment":{"API":"http://host.docker.internal:3000"},"extra_hosts":["host.docker.internal=host-gateway"]},
		"mapped": {"command":["curl","host.docker.internal"],"extra_hosts":{"host.docker.internal":["host-gateway"]}},
		"plain":  {"image":"redis"}}}`)
	wantOne(t, findings(t, "host-gateway", f), Fail, "in: web")

	f.Info.OperatingSystem = "Docker Desktop"
	if _, ran := checkHostGateway(f); ran {
		t.Error("host-gateway check ran against Docker Desktop, which defines the name")
	}

	f.Info.OperatingSystem = "Ubuntu 24.04"
	f.GOOS = "darwin"
	if _, ran := checkHostGateway(f); ran {
		t.Error("host-gateway check ran on macOS")
	}
}

func TestPortsInUse(t *testing.T) {
	f := linuxFacts()
	f.Project = project(t, `{"name":"app","services":{
		"web":{"ports":[{"target":80,"published":"8080"}]},
		"dns":{"ports":[{"target":53,"published":"53","protocol":"udp"}]},
		"api":{"ports":[{"target":3000,"published":"3000"}]}}}`)
	f.PortInUse = func(port int, proto string) bool {
		return port == 8080 || (port == 53 && proto == "udp") || port == 3000
	}
	// The project's own running api container holds 3000; that's expected.
	f.Containers = []Container{container(t, `{"Name":"/app-api-1","Config":{"Labels":{"com.docker.compose.project":"app"}},"State":{"Running":true},"NetworkSettings":{"Ports":{"3000/tcp":[{"HostIp":"0.0.0.0","HostPort":"3000"}]}}}`)}

	got := findings(t, "ports", f)
	if len(got) != 2 {
		t.Fatalf("got %+v, want ports 53 and 8080", got)
	}
	if !strings.Contains(got[0].Title, "Port 53") || !strings.Contains(got[0].Fix[0], "-lunp") {
		t.Errorf("udp finding = %+v", got[0])
	}
	if !strings.Contains(got[1].Title, "Port 8080") {
		t.Errorf("tcp finding = %+v", got[1])
	}
}

func TestBindMounts(t *testing.T) {
	f := linuxFacts()
	existing := map[string]bool{"/home/ada/app/data": true}
	f.Exists = func(p string) bool { return existing[p] }
	f.Project = project(t, `{"name":"app","services":{"web":{"volumes":[
		{"type":"bind","source":"/home/ada/app/data","target":"/data"},
		{"type":"bind","source":"/home/ada/app/nginx.conf","target":"/etc/nginx/nginx.conf","bind":{"create_host_path":true}},
		{"type":"bind","source":"/home/ada/app/strict","target":"/s","bind":{"create_host_path":false}},
		{"type":"bind","source":"/home/ada/app/uploads","target":"/u","bind":{"create_host_path":true}},
		{"type":"volume","source":"dbdata","target":"/var/lib/postgresql/data"}]}}}`)
	got := findings(t, "bind-mounts", f)
	if len(got) != 3 {
		t.Fatalf("got %d findings: %+v", len(got), got)
	}
	if got[0].Severity != Fail || !strings.Contains(got[0].Title, "as a directory") {
		t.Errorf("missing file = %+v", got[0])
	}
	if got[1].Severity != Fail || !strings.Contains(got[1].Title, "strict") {
		t.Errorf("create_host_path false = %+v", got[1])
	}
	if got[2].Severity != Info || !strings.Contains(got[2].Title, "uploads") {
		t.Errorf("missing dir = %+v", got[2])
	}
}

func TestBindMountsSnapOutsideHome(t *testing.T) {
	f := linuxFacts()
	f.DockerPath = "/snap/docker/2915/bin/docker"
	f.Project = project(t, `{"name":"nomad","services":{"admin":{"volumes":[
		{"type":"bind","source":"/opt/project-nomad","target":"/app/storage"},
		{"type":"bind","source":"/home/ada/nomad","target":"/x"},
		{"type":"bind","source":"/mnt/disk/media","target":"/m"}]}}}`)
	wantOne(t, findings(t, "bind-mounts", f), Fail, "/opt/project-nomad")
}

func TestRootlessPorts(t *testing.T) {
	f := linuxFacts()
	f.Info.SecurityOptions = []string{"name=seccomp,profile=builtin", "name=rootless"}
	f.Project = project(t, `{"name":"app","services":{"proxy":{"ports":[{"target":80,"published":"80"},{"target":443,"published":"443"},{"target":8080,"published":"8080"}]}}}`)
	fd := wantOne(t, findings(t, "rootless-ports", f), Fail, "80 (proxy), 443 (proxy)")
	if !strings.Contains(fd.Fix[0], "=80'") {
		t.Errorf("Fix = %v, want the lowest port", fd.Fix)
	}

	f.UnprivPortStart = 80
	if got := findings(t, "rootless-ports", f); len(got) != 0 {
		t.Errorf("with the sysctl lowered: %+v", got)
	}
}

func TestFailuresSortFirst(t *testing.T) {
	f := linuxFacts()
	f.DiskFree = 1 * gib
	f.Containers = []Container{container(t, `{"Name":"/x","State":{"OOMKilled":true}}`)}
	r := Run(f)
	if len(r.Findings) != 2 || r.Findings[0].Severity != Fail || r.Findings[1].Severity != Warn {
		t.Fatalf("order = %+v", r.Findings)
	}
}
