package doctor

import (
	"context"
	"strings"
	"testing"
)

func archFacts(t *testing.T) *Facts {
	f := linuxFacts()
	f.Info.Architecture = "aarch64"
	f.Images = map[string]ImageInfo{}
	for _, img := range []ImageInfo{
		{ID: "sha256:amd", RepoTags: []string{"example/app:1.0"}, Architecture: "amd64"},
		{ID: "sha256:arm", RepoTags: []string{"redis:7"}, Architecture: "arm64"},
	} {
		f.Images[img.ID] = img
		for _, tag := range img.RepoTags {
			f.Images[tag] = img
		}
	}
	return f
}

func TestArchMismatchWithoutEmulation(t *testing.T) {
	f := archFacts(t)
	f.Containers = []Container{
		container(t, `{"Name":"/app","Image":"sha256:amd","Config":{"Image":"example/app:1.0"},"State":{"Restarting":true,"ExitCode":139},"RestartCount":7}`),
		container(t, `{"Name":"/cache","Image":"sha256:arm","Config":{"Image":"redis:7"},"State":{"Running":true}}`),
	}
	fd := wantOne(t, findings(t, "arch", f), Fail, "example/app:1.0 is built for amd64, not arm64 (app)")
	if !strings.Contains(fd.Fix[2], "--install amd64") {
		t.Errorf("Fix = %v", fd.Fix)
	}

	// The restart-loop finding points at the cause too.
	restart := wantOne(t, findings(t, "restarts", f), Fail, "restart loop")
	if !strings.Contains(restart.Why, "built for amd64 but this host is arm64") {
		t.Errorf("restart Why = %q", restart.Why)
	}
}

func TestArchEmulatedIsANote(t *testing.T) {
	f := archFacts(t)
	f.Emulated = emulatedArchs([]string{"qemu-x86_64", "register", "status"})
	f.Containers = []Container{
		container(t, `{"Name":"/app","Image":"sha256:amd","Config":{"Image":"example/app:1.0"},"State":{"Running":true}}`),
	}
	wantOne(t, findings(t, "arch", f), Info, "1 image runs under emulation")

	// Docker Desktop emulates on its own.
	f.Emulated = nil
	f.Info.OperatingSystem = "Docker Desktop"
	wantOne(t, findings(t, "arch", f), Info, "emulation")
}

func TestArchComposeServiceNotStartedYet(t *testing.T) {
	f := archFacts(t)
	f.Project = project(t, `{"name":"app","services":{
		"web":   {"image":"example/app:1.0"},
		"pinned":{"image":"example/app:1.0","platform":"linux/amd64"},
		"cache": {"image":"redis:7"},
		"new":   {"image":"not/pulled"}}}`)
	wantOne(t, findings(t, "arch", f), Fail, "(web)")
}

func TestArchFromManifestDescriptor(t *testing.T) {
	// containerd image store: the image has no Architecture, the container's
	// manifest descriptor says what it runs.
	f := linuxFacts()
	f.Info.Architecture = "aarch64"
	f.Info.OperatingSystem = "Docker Desktop"
	f.Images = map[string]ImageInfo{"sha256:idx": {ID: "sha256:idx", RepoTags: []string{"busybox:1.36"}}}
	f.Containers = []Container{
		container(t, `{"Name":"/x","Image":"sha256:idx","Config":{"Image":"busybox:1.36"},"State":{"Running":true},
			"ImageManifestDescriptor":{"platform":{"architecture":"amd64","os":"linux"}}}`),
		container(t, `{"Name":"/y","Image":"sha256:idx","Config":{"Image":"busybox:1.36"},"State":{"Running":true},
			"ImageManifestDescriptor":{"platform":{"architecture":"arm64","os":"linux"}}}`),
	}
	fd := wantOne(t, findings(t, "arch", f), Info, "emulation")
	if !strings.Contains(fd.Why, "busybox:1.36 (amd64, used by x)") {
		t.Errorf("Why = %q", fd.Why)
	}
}

func TestArchSkipsWithoutImages(t *testing.T) {
	f := linuxFacts()
	f.Info.Architecture = "x86_64"
	if _, ran := checkArch(f); ran {
		t.Error("arch check ran without image data")
	}
}

func TestImageRef(t *testing.T) {
	cases := map[string]string{
		"nginx":              "nginx:latest",
		"nginx:1.27":         "nginx:1.27",
		"localhost:5000/app": "localhost:5000/app:latest",
		"ghcr.io/org/app:v2": "ghcr.io/org/app:v2",
		"redis@sha256:abc":   "redis@sha256:abc",
	}
	for in, want := range cases {
		if got := imageRef(in); got != want {
			t.Errorf("imageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogsUnbounded(t *testing.T) {
	f := linuxFacts()
	f.Containers = []Container{
		container(t, `{"Name":"/chatty","HostConfig":{"LogConfig":{"Type":"json-file","Config":{}}}}`),
		container(t, `{"Name":"/quiet","HostConfig":{"LogConfig":{"Type":"json-file","Config":{}}}}`),
		container(t, `{"Name":"/capped","HostConfig":{"LogConfig":{"Type":"json-file","Config":{"max-size":"10m"}}}}`),
		container(t, `{"Name":"/local","HostConfig":{"LogConfig":{"Type":"local","Config":{}}}}`),
		container(t, `{"Name":"/journal","HostConfig":{"LogConfig":{"Type":"journald"}}}`),
	}
	fd := wantOne(t, findings(t, "logs", f), Info, "2 containers keep logs with no size limit: chatty, quiet")
	if !strings.Contains(fd.Fix[0], "/etc/docker/daemon.json") {
		t.Errorf("Fix = %v", fd.Fix)
	}

	// A log over 1 GiB makes it a warning and is named.
	f.LogSize = map[string]int64{"chatty": 3 * gib, "quiet": 2 << 20}
	fd = wantOne(t, findings(t, "logs", f), Warn, "no size limit")
	if !strings.Contains(fd.Why, "chatty 3.0 GiB, quiet 2 MiB") {
		t.Errorf("Why = %q", fd.Why)
	}
}

func TestLogsWithLowDisk(t *testing.T) {
	f := linuxFacts()
	f.DiskFree = 1 * gib
	f.Info.OperatingSystem = "Docker Desktop"
	f.Containers = []Container{container(t, `{"Name":"/a","HostConfig":{"LogConfig":{"Type":"json-file"}}}`)}

	fd := wantOne(t, findings(t, "logs", f), Warn, "1 container keeps")
	if !strings.Contains(fd.Fix[0], "Docker Desktop") {
		t.Errorf("Fix = %v, want Docker Desktop instructions", fd.Fix)
	}
	disk := wantOne(t, findings(t, "disk", f), Warn, "Low disk")
	if last := disk.Fix[len(disk.Fix)-1]; !strings.Contains(last, "1 container has no limit") {
		t.Errorf("disk Fix = %v", disk.Fix)
	}
}

func TestLogsAllBounded(t *testing.T) {
	f := linuxFacts()
	f.Containers = []Container{container(t, `{"Name":"/a","HostConfig":{"LogConfig":{"Type":"local"}}}`)}
	if got := findings(t, "logs", f); len(got) != 0 {
		t.Errorf("findings = %+v", got)
	}
}

func TestGatherImagesAndLogs(t *testing.T) {
	fs := fakeSystem{
		paths: map[string]string{"docker": "/usr/bin/docker"},
		cmds: map[string]fakeOut{
			"docker info --format {{json .}}": {stdout: `{"ServerVersion":"27.3.1","Architecture":"x86_64"}`},
			"docker compose version --short":  {stdout: "2.29.7"},
			"docker ps -aq":                   {stdout: "abc\n"},
			"docker inspect abc":              {stdout: `[{"Name":"/web","Image":"sha256:1","LogPath":"/var/lib/docker/containers/abc/abc-json.log","HostConfig":{"LogConfig":{"Type":"json-file"}}}]`},
			// One image is missing: Docker prints the others and exits 1.
			"docker image inspect sha256:1": {stdout: `[{"Id":"sha256:1","RepoTags":["nginx:latest"],"Architecture":"arm64"}]`},
		},
	}
	sys := fs.system()
	sys.ListDir = func(string) ([]string, error) { return []string{"qemu-aarch64", "status"}, nil }
	sys.FileSize = func(string) (int64, error) { return 42, nil }
	f := Gather(context.Background(), sys, nil)

	if f.Images["nginx:latest"].Architecture != "arm64" || f.Images["sha256:1"].ID != "sha256:1" {
		t.Errorf("Images = %+v", f.Images)
	}
	if !f.Emulated["arm64"] {
		t.Errorf("Emulated = %v", f.Emulated)
	}
	if f.LogSize["web"] != 42 {
		t.Errorf("LogSize = %v", f.LogSize)
	}
	if f.foreignArch("sha256:1") != "arm64" {
		t.Errorf("foreignArch = %q", f.foreignArch("sha256:1"))
	}
}
