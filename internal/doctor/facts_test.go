package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSystem answers commands from a table keyed by the joined command line.
type fakeSystem struct {
	cmds  map[string]fakeOut
	files map[string]string
	paths map[string]string
}

type fakeOut struct {
	stdout, stderr string
	err            error
}

func (fs fakeSystem) system() System {
	return System{
		GOOS: "linux",
		Home: "/home/ada",
		Dir:  "/home/ada/app",
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			out, ok := fs.cmds[key]
			if !ok {
				return "", "unknown command: " + key, errors.New("exit status 1")
			}
			return out.stdout, out.stderr, out.err
		},
		LookPath: func(file string) (string, error) {
			if p, ok := fs.paths[file]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		ReadFile: func(name string) ([]byte, error) {
			if s, ok := fs.files[name]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("no such file")
		},
		Exists: func(path string) bool {
			_, ok := fs.files[path]
			return ok
		},
		PortInUse: func(int, string) bool { return false },
		DiskUsage: func(string) (uint64, uint64, error) { return 50 * gib, 100 * gib, nil },
	}
}

const composeJSON = `{
  "name": "app",
  "services": {
    "web": {
      "image": "nginx",
      "environment": {"API_URL": "http://host.docker.internal:3000"},
      "ports": [{"mode": "ingress", "target": 80, "published": "8080", "protocol": "tcp"}],
      "volumes": [{"type": "bind", "source": "/opt/app/nginx.conf", "target": "/etc/nginx/nginx.conf", "bind": {"create_host_path": true}}]
    },
    "db": {
      "image": "postgres",
      "ports": [{"target": 5432, "published": 5432}]
    }
  }
}`

func TestGatherReadsEverything(t *testing.T) {
	fs := fakeSystem{
		paths: map[string]string{"docker": "/usr/bin/docker"},
		cmds: map[string]fakeOut{
			"docker info --format {{json .}}": {stdout: `{"ServerVersion":"27.3.1","OperatingSystem":"Ubuntu 24.04","DockerRootDir":"/var/lib/docker","SecurityOptions":["name=seccomp"]}`},
			"docker compose version --short":  {stdout: "2.29.7\n"},
			"docker ps -aq":                   {stdout: "abc\ndef\n"},
			"docker inspect abc def":          {stdout: `[{"Name":"/web","State":{"Status":"running","Running":true}},{"Name":"/worker","State":{"Status":"restarting","Restarting":true,"ExitCode":127},"RestartCount":9}]`},
			"docker compose -f /home/ada/app/compose.yaml config --format json": {stdout: composeJSON, stderr: `time="2026-11-22T10:00:00Z" level=warning msg="The \"DB_PASSWORD\" variable is not set. Defaulting to a blank string."` + "\n"},
		},
		files: map[string]string{
			"/home/ada/app/compose.yaml":                    "",
			"/etc/ufw/ufw.conf":                             "# comment\nENABLED=yes\n",
			"/proc/sys/net/ipv4/ip_unprivileged_port_start": "1024\n",
		},
	}
	f := Gather(context.Background(), fs.system(), nil)

	if f.Info == nil || f.Info.ServerVersion != "27.3.1" {
		t.Fatalf("Info = %+v, want server 27.3.1 (daemon err %q)", f.Info, f.DaemonErr)
	}
	if f.ComposeVersion != "2.29.7" {
		t.Errorf("ComposeVersion = %q", f.ComposeVersion)
	}
	if len(f.Containers) != 2 || f.Containers[1].ShortName() != "worker" || f.Containers[1].RestartCount != 9 {
		t.Errorf("Containers = %+v", f.Containers)
	}
	if f.Project == nil || len(f.Project.Services) != 2 {
		t.Fatalf("Project = %+v, err %q", f.Project, f.ComposeErr)
	}
	if got := f.Project.Services["db"].Ports[0].HostPorts(); len(got) != 1 || got[0] != 5432 {
		t.Errorf("numeric published port = %v, want [5432]", got)
	}
	if len(f.ComposeWarnings) != 1 || f.ComposeWarnings[0] != "DB_PASSWORD" {
		t.Errorf("ComposeWarnings = %v", f.ComposeWarnings)
	}
	if !f.UFWEnabled || f.UnprivPortStart != 1024 {
		t.Errorf("UFWEnabled = %v, UnprivPortStart = %d", f.UFWEnabled, f.UnprivPortStart)
	}
	if f.DiskTotal != 100*gib {
		t.Errorf("DiskTotal = %d", f.DiskTotal)
	}
}

func TestGatherDaemonDown(t *testing.T) {
	fs := fakeSystem{
		paths: map[string]string{"docker": "/usr/bin/docker"},
		cmds: map[string]fakeOut{
			"docker info --format {{json .}}": {
				stdout: `{"ServerErrors":["Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"]}`,
				err:    errors.New("exit status 1"),
			},
		},
	}
	f := Gather(context.Background(), fs.system(), nil)
	if f.Info != nil {
		t.Fatalf("Info = %+v, want nil", f.Info)
	}
	if !strings.Contains(f.DaemonErr, "Is the docker daemon running") {
		t.Errorf("DaemonErr = %q", f.DaemonErr)
	}
}

func TestGatherComposeV1Only(t *testing.T) {
	fs := fakeSystem{
		paths: map[string]string{"docker": "/usr/bin/docker", "docker-compose": "/usr/bin/docker-compose"},
		cmds: map[string]fakeOut{
			"docker info --format {{json .}}": {stdout: `{"ServerVersion":"20.10.24"}`},
			"docker-compose version --short":  {stdout: "1.29.2\n"},
		},
	}
	f := Gather(context.Background(), fs.system(), nil)
	if !f.ComposeV1 || f.ComposeVersion != "" {
		t.Errorf("ComposeV1 = %v, ComposeVersion = %q", f.ComposeV1, f.ComposeVersion)
	}
}

func TestGatherStandaloneComposeV2(t *testing.T) {
	fs := fakeSystem{
		paths: map[string]string{"docker": "/usr/bin/docker", "docker-compose": "/usr/local/bin/docker-compose"},
		cmds: map[string]fakeOut{
			"docker info --format {{json .}}": {stdout: `{"ServerVersion":"27.0.0"}`},
			"docker-compose version --short":  {stdout: "v2.20.0\n"},
		},
	}
	f := Gather(context.Background(), fs.system(), nil)
	if f.ComposeV1 || f.ComposeVersion != "2.20.0" {
		t.Errorf("ComposeV1 = %v, ComposeVersion = %q", f.ComposeV1, f.ComposeVersion)
	}
}

func TestUnsetVariables(t *testing.T) {
	stderr := `WARN[0000] The "A" variable is not set. Defaulting to a blank string.
time="2026" level=warning msg="The \"B\" variable is not set. Defaulting to a blank string."
WARN[0000] The "A" variable is not set. Defaulting to a blank string.
WARN[0000] /x/compose.yaml: the attribute ` + "`version`" + ` is obsolete`
	got := unsetVariables(stderr)
	if strings.Join(got, ",") != "A,B" {
		t.Errorf("unsetVariables = %v, want [A B]", got)
	}
}

func TestHostPortsRange(t *testing.T) {
	p := Port{Published: "8000-8002"}
	if got := p.HostPorts(); len(got) != 3 || got[0] != 8000 || got[2] != 8002 {
		t.Errorf("HostPorts = %v", got)
	}
	if got := (Port{}).HostPorts(); got != nil {
		t.Errorf("unpublished HostPorts = %v, want nil", got)
	}
}

func TestUFWEnabled(t *testing.T) {
	cases := map[string]bool{
		"ENABLED=yes":              true,
		"ENABLED=\"yes\"":          true,
		"ENABLED=no":               false,
		"#ENABLED=yes\nENABLED=no": false,
		"":                         false,
	}
	for conf, want := range cases {
		if got := ufwEnabled(conf); got != want {
			t.Errorf("ufwEnabled(%q) = %v, want %v", conf, got, want)
		}
	}
}
