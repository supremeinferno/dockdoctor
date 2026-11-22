// Package doctor collects facts about a Docker setup and checks them for
// problems that commonly break self-hosted apps.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// System is everything Gather needs from the machine. Tests replace it.
type System struct {
	GOOS      string
	Home      string
	Dir       string
	Run       func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
	LookPath  func(file string) (string, error)
	ReadFile  func(name string) ([]byte, error)
	Exists    func(path string) bool
	PortInUse func(port int, proto string) bool
	DiskUsage func(path string) (free, total uint64, err error)
}

// RealSystem returns a System backed by the running machine.
func RealSystem() System {
	home, _ := os.UserHomeDir()
	dir, _ := os.Getwd()
	return System{
		GOOS: runtime.GOOS,
		Home: home,
		Dir:  dir,
		Run: func(ctx context.Context, name string, args ...string) (string, string, error) {
			var out, errb bytes.Buffer
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			return out.String(), errb.String(), err
		},
		LookPath: exec.LookPath,
		ReadFile: os.ReadFile,
		Exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		PortInUse: portInUse,
		DiskUsage: diskUsage,
	}
}

// portInUse reports whether something on this machine already holds port.
// IPv4 and IPv6 are tried separately because some systems (macOS) let a
// dual-stack listener bind next to an IPv4-only one. Only "address in use"
// counts: a permission error on a low port says nothing either way.
func portInUse(port int, proto string) bool {
	p := strconv.Itoa(port)
	if proto == "udp" {
		for _, n := range [][2]string{{"udp4", "0.0.0.0:"}, {"udp6", "[::]:"}} {
			c, err := net.ListenPacket(n[0], n[1]+p)
			if err != nil {
				if errors.Is(err, syscall.EADDRINUSE) {
					return true
				}
				continue
			}
			c.Close()
		}
		return false
	}
	// A listener on localhost answers a connect even when binding looks free.
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 300*time.Millisecond); err == nil {
		c.Close()
		return true
	}
	for _, n := range [][2]string{{"tcp4", "0.0.0.0:"}, {"tcp6", "[::]:"}} {
		l, err := net.Listen(n[0], n[1]+p)
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				return true
			}
			continue
		}
		l.Close()
	}
	return false
}

// Facts is what dockdoctor learned about the machine. Checks only read it.
type Facts struct {
	GOOS string
	Home string
	Dir  string // where dockdoctor runs; paths under it are shown relative

	DockerPath string // resolved path of the docker CLI, "" when not installed
	DaemonErr  string // why the daemon could not be reached, "" when it answered
	Info       *DockerInfo

	ComposeVersion string // version of Compose v2, "" when missing
	ComposeV1      bool   // only the old Python docker-compose is installed
	composeCmd     []string

	Containers []Container

	ComposeFiles    []string
	Project         *Project
	ComposeErr      string
	ComposeWarnings []string

	UFWEnabled      bool
	UnprivPortStart int // net.ipv4.ip_unprivileged_port_start, 0 when unknown

	DiskFree, DiskTotal uint64

	PortInUse func(port int, proto string) bool
	Exists    func(path string) bool
}

// DockerInfo holds the parts of `docker info` the checks use.
type DockerInfo struct {
	ServerVersion   string   `json:"ServerVersion"`
	OperatingSystem string   `json:"OperatingSystem"`
	DockerRootDir   string   `json:"DockerRootDir"`
	SecurityOptions []string `json:"SecurityOptions"`
	ServerErrors    []string `json:"ServerErrors"`
}

// Rootless reports whether the daemon runs in rootless mode.
func (i *DockerInfo) Rootless() bool {
	for _, opt := range i.SecurityOptions {
		if strings.Contains(opt, "name=rootless") {
			return true
		}
	}
	return false
}

// Container holds the parts of `docker inspect` the checks use.
type Container struct {
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		Health     *struct {
			Status string `json:"Status"`
			Log    []struct {
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount    int `json:"RestartCount"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// ShortName is the container name without Docker's leading slash.
func (c Container) ShortName() string { return strings.TrimPrefix(c.Name, "/") }

// Project is the parts of `docker compose config --format json` the checks use.
type Project struct {
	Name     string
	Services map[string]Service
}

// Service is one Compose service.
type Service struct {
	Ports      []Port          `json:"ports"`
	Volumes    []Volume        `json:"volumes"`
	ExtraHosts json.RawMessage `json:"extra_hosts"`
	raw        map[string]json.RawMessage
}

// Port is one published port of a service.
type Port struct {
	Target    int        `json:"target"`
	Published flexString `json:"published"`
	Protocol  string     `json:"protocol"`
	HostIP    string     `json:"host_ip"`
}

// Volume is one mount of a service.
type Volume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
	Bind   *struct {
		CreateHostPath bool `json:"create_host_path"`
	} `json:"bind"`
}

// flexString accepts a JSON string or number; Compose versions differ.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}

// HostPorts expands the published port, or range, into host port numbers.
func (p Port) HostPorts() []int {
	s := strings.TrimSpace(string(p.Published))
	if s == "" {
		return nil
	}
	lo, hi, isRange := strings.Cut(s, "-")
	a, err := strconv.Atoi(lo)
	if err != nil {
		return nil
	}
	if !isRange {
		return []int{a}
	}
	b, err := strconv.Atoi(hi)
	if err != nil || b < a || b-a > 1000 {
		return []int{a}
	}
	ports := make([]int, 0, b-a+1)
	for i := a; i <= b; i++ {
		ports = append(ports, i)
	}
	return ports
}

func (p Port) proto() string {
	if p.Protocol == "" {
		return "tcp"
	}
	return p.Protocol
}

// mentions reports whether the service config contains s anywhere other
// than in extra_hosts.
func (s Service) mentions(needle string) bool {
	for key, value := range s.raw {
		if key == "extra_hosts" {
			continue
		}
		if bytes.Contains(value, []byte(needle)) {
			return true
		}
	}
	return false
}

// ParseProject reads the JSON printed by `docker compose config --format json`.
func ParseProject(data []byte) (*Project, error) {
	var top struct {
		Name     string                     `json:"name"`
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	p := &Project{Name: top.Name, Services: map[string]Service{}}
	for name, raw := range top.Services {
		var svc Service
		if err := json.Unmarshal(raw, &svc); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &svc.raw); err != nil {
			return nil, err
		}
		p.Services[name] = svc
	}
	return p, nil
}

// composeFileNames are the files `docker compose` picks up by default.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// Gather collects facts. composeFiles may be empty, in which case a Compose
// file in sys.Dir is used when there is one.
func Gather(ctx context.Context, sys System, composeFiles []string) *Facts {
	f := &Facts{GOOS: sys.GOOS, Home: sys.Home, Dir: sys.Dir, PortInUse: sys.PortInUse, Exists: sys.Exists}

	path, err := sys.LookPath("docker")
	if err != nil {
		return f
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	f.DockerPath = path

	gatherInfo(ctx, sys, f)
	gatherCompose(ctx, sys, f)
	if f.Info != nil {
		gatherContainers(ctx, sys, f)
	}
	gatherProject(ctx, sys, f, composeFiles)

	if sys.GOOS == "linux" {
		if data, err := sys.ReadFile("/etc/ufw/ufw.conf"); err == nil {
			f.UFWEnabled = ufwEnabled(string(data))
		}
		if data, err := sys.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start"); err == nil {
			f.UnprivPortStart, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if f.Info != nil && f.Info.DockerRootDir != "" && sys.DiskUsage != nil {
			if free, total, err := sys.DiskUsage(f.Info.DockerRootDir); err == nil {
				f.DiskFree, f.DiskTotal = free, total
			}
		}
	}
	return f
}

func gatherInfo(ctx context.Context, sys System, f *Facts) {
	out, errOut, err := sys.Run(ctx, "docker", "info", "--format", "{{json .}}")
	var info DockerInfo
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); jsonErr == nil && info.ServerVersion != "" {
		f.Info = &info
		return
	}
	switch {
	case len(info.ServerErrors) > 0:
		f.DaemonErr = strings.Join(info.ServerErrors, "\n")
	case strings.TrimSpace(errOut) != "":
		f.DaemonErr = strings.TrimSpace(errOut)
	case err != nil:
		f.DaemonErr = err.Error()
	default:
		f.DaemonErr = "docker info returned no server details"
	}
}

func gatherCompose(ctx context.Context, sys System, f *Facts) {
	if out, _, err := sys.Run(ctx, "docker", "compose", "version", "--short"); err == nil && strings.TrimSpace(out) != "" {
		f.ComposeVersion = strings.TrimPrefix(strings.TrimSpace(out), "v")
		f.composeCmd = []string{"docker", "compose"}
		return
	}
	if _, err := sys.LookPath("docker-compose"); err != nil {
		return
	}
	out, _, err := sys.Run(ctx, "docker-compose", "version", "--short")
	version := strings.TrimPrefix(strings.TrimSpace(out), "v")
	if err == nil && strings.HasPrefix(version, "2.") {
		// The standalone v2 binary works the same as the plugin.
		f.ComposeVersion = version
		f.composeCmd = []string{"docker-compose"}
		return
	}
	f.ComposeV1 = true
}

func gatherContainers(ctx context.Context, sys System, f *Facts) {
	out, _, err := sys.Run(ctx, "docker", "ps", "-aq")
	if err != nil {
		return
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return
	}
	out, _, err = sys.Run(ctx, "docker", append([]string{"inspect"}, ids...)...)
	if err != nil {
		return
	}
	_ = json.Unmarshal([]byte(out), &f.Containers)
}

func gatherProject(ctx context.Context, sys System, f *Facts, files []string) {
	if len(files) == 0 {
		for _, name := range composeFileNames {
			if p := filepath.Join(sys.Dir, name); sys.Exists(p) {
				files = []string{p}
				break
			}
		}
	}
	if len(files) == 0 {
		return
	}
	f.ComposeFiles = files
	if f.composeCmd == nil {
		f.ComposeErr = "Compose v2 is not installed, so the Compose file was not checked"
		return
	}
	args := append([]string{}, f.composeCmd[1:]...)
	for _, file := range files {
		args = append(args, "-f", file)
	}
	args = append(args, "config", "--format", "json")
	out, errOut, err := sys.Run(ctx, f.composeCmd[0], args...)
	f.ComposeWarnings = unsetVariables(errOut)
	if err != nil {
		f.ComposeErr = strings.TrimSpace(errOut)
		if f.ComposeErr == "" {
			f.ComposeErr = err.Error()
		}
		return
	}
	p, err := ParseProject([]byte(out))
	if err != nil {
		f.ComposeErr = "could not read `docker compose config` output: " + err.Error()
		return
	}
	f.Project = p
}

// unsetVariables pulls the variable names out of Compose's
// `The "X" variable is not set` warnings.
func unsetVariables(stderr string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "variable is not set") {
			continue
		}
		line = strings.ReplaceAll(line, `\"`, `"`)
		start := strings.Index(line, `The "`)
		if start < 0 {
			continue
		}
		rest := line[start+len(`The "`):]
		end := strings.Index(rest, `"`)
		if end <= 0 {
			continue
		}
		name := rest[:end]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func ufwEnabled(conf string) bool {
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == "ENABLED" {
			return strings.EqualFold(strings.Trim(strings.TrimSpace(value), `"'`), "yes")
		}
	}
	return false
}
