package doctor

import (
	"fmt"
	"sort"
	"strings"
)

func (f *Facts) hostArch() string {
	if f.Info == nil {
		return ""
	}
	return normArch(f.Info.Architecture)
}

// canEmulate reports whether images for arch can run on this host anyway.
// Docker Desktop and OrbStack ship emulation; on Linux it takes a binfmt_misc
// handler such as the ones tonistiigi/binfmt installs.
func (f *Facts) canEmulate(arch string) bool {
	return f.definesHostGateway() || f.Emulated[arch]
}

// foreignArch returns the image's architecture when it differs from the host's.
func (f *Facts) foreignArch(ref string) string {
	img, ok := f.Images[ref]
	if !ok {
		return ""
	}
	return f.differs(img.Architecture)
}

// containerArch returns the architecture a container runs, when it differs
// from the host's.
func (f *Facts) containerArch(c Container) string {
	if d := c.ImageManifestDescriptor; d != nil && d.Platform != nil && d.Platform.Architecture != "" {
		return f.differs(d.Platform.Architecture)
	}
	return f.foreignArch(c.Image)
}

func (f *Facts) differs(arch string) string {
	host, arch := f.hostArch(), normArch(arch)
	if host == "" || arch == "" || arch == host {
		return ""
	}
	return arch
}

type foreignImage struct {
	ref, arch string
	users     []string // containers or services using it
	broken    bool     // a container using it isn't running
}

func checkArch(f *Facts) ([]Finding, bool) {
	if !f.daemonUp() || f.hostArch() == "" || (f.Images == nil && len(f.Containers) == 0) {
		return nil, false
	}
	byRef := map[string]*foreignImage{}
	note := func(ref, arch, user string, broken bool) {
		fi := byRef[ref]
		if fi == nil {
			fi = &foreignImage{ref: ref, arch: arch}
			byRef[ref] = fi
		}
		fi.users = append(fi.users, user)
		fi.broken = fi.broken || broken
	}

	created := map[string]bool{} // Compose services that already have a container
	for _, c := range f.Containers {
		if svc := c.Config.Labels["com.docker.compose.service"]; svc != "" && f.Project != nil &&
			c.Config.Labels["com.docker.compose.project"] == f.Project.Name {
			created[svc] = true
		}
		if arch := f.containerArch(c); arch != "" {
			broken := c.State.Restarting || (!c.State.Running && c.State.ExitCode != 0)
			note(c.Config.Image, arch, c.ShortName(), broken)
		}
	}
	if f.Project != nil {
		for _, name := range sortedServices(f.Project) {
			svc := f.Project.Services[name]
			if created[name] || svc.Platform != "" {
				continue // its container is listed above, or the platform was chosen on purpose
			}
			if arch := f.foreignArch(imageRef(svc.Image)); arch != "" {
				note(imageRef(svc.Image), arch, name, false)
			}
		}
	}
	if len(byRef) == 0 {
		return nil, true
	}

	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	host := f.hostArch()
	var findings []Finding
	var emulated []string
	for _, ref := range refs {
		fi := byRef[ref]
		users := strings.Join(dedupe(fi.users), ", ")
		if f.canEmulate(fi.arch) && !fi.broken {
			emulated = append(emulated, fmt.Sprintf("%s (%s, used by %s)", ref, fi.arch, users))
			continue
		}
		why := fmt.Sprintf("This host is %s and the image only has %s, so the kernel can't run its programs: "+
			"the container exits with `exec format error`, code 139, or keeps restarting.", host, fi.arch)
		if f.canEmulate(fi.arch) {
			why = fmt.Sprintf("This host is %s and the image is %s. Emulation is available, but a container using it still isn't running, "+
				"and some programs don't work under emulation.", host, fi.arch)
		}
		findings = append(findings, Finding{
			Severity: Fail,
			Title:    fmt.Sprintf("%s is built for %s, not %s (%s)", ref, fi.arch, host, users),
			Why:      why,
			Fix: []string{
				"docker buildx imagetools inspect " + ref + "    # which platforms the tag has",
				"# use a tag or image that has linux/" + host + ", or as a slow fallback install emulation:",
				"docker run --privileged --rm tonistiigi/binfmt --install " + fi.arch,
			},
			Docs: "https://docs.docker.com/build/building/multi-platform/",
		})
	}
	if len(emulated) > 0 {
		findings = append(findings, Finding{
			Severity: Info,
			Title:    fmt.Sprintf("%s under emulation on this %s host", pluralVerb(len(emulated), "image runs", "images run"), host),
			Why: "They work, but slower, and the odd program misbehaves under emulation:\n" + strings.Join(emulated, "\n") +
				"\nIf the project publishes " + host + " images, use those.",
			Fix: []string{"docker buildx imagetools inspect <image>    # which platforms a tag has"},
		})
	}
	return findings, true
}
