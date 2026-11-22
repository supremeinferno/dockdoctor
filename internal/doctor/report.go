package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Summary is the one-line description of what was checked.
func (f *Facts) Summary() string {
	var parts []string
	switch {
	case f.Info != nil:
		s := "Docker " + f.Info.ServerVersion
		if f.Info.OperatingSystem != "" {
			s += " on " + f.Info.OperatingSystem
		}
		if f.Info.Rootless() {
			s += " (rootless)"
		}
		parts = append(parts, s)
	case f.DockerPath != "":
		parts = append(parts, "Docker CLI found, daemon not reachable")
	default:
		parts = append(parts, "Docker not found")
	}
	if f.ComposeVersion != "" {
		parts = append(parts, "Compose "+f.ComposeVersion)
	}
	if len(f.ComposeFiles) > 0 {
		names := make([]string, len(f.ComposeFiles))
		for i, p := range f.ComposeFiles {
			names[i] = filepath.Base(p)
		}
		s := strings.Join(names, ", ")
		if f.Project != nil && f.Project.Name != "" {
			s = fmt.Sprintf("project %q (%s)", f.Project.Name, s)
		}
		parts = append(parts, s)
	}
	if n := len(f.Containers); n > 0 {
		parts = append(parts, fmt.Sprintf("%d containers", n))
	}
	return strings.Join(parts, " · ")
}

// Style decides how text output looks.
type Style struct {
	Color   bool
	Verbose bool // also list the checks that passed or were skipped
}

func (s Style) paint(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

var marks = map[Severity]struct{ symbol, color string }{
	Fail: {"✗", "31"},
	Warn: {"!", "33"},
	Info: {"i", "36"},
}

// WriteText prints the report for a terminal.
func WriteText(w io.Writer, f *Facts, r Result, s Style) {
	fmt.Fprintf(w, "%s %s\n\n", s.paint("1", "dockdoctor"), s.paint("2", f.Summary()))

	for _, fd := range r.Findings {
		m := marks[fd.Severity]
		fmt.Fprintf(w, "%s %s\n", s.paint(m.color+";1", m.symbol), s.paint("1", fd.Title))
		for _, line := range strings.Split(fd.Why, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
		if len(fd.Fix) > 0 {
			fmt.Fprintf(w, "  %s\n", s.paint("32", "Fix:"))
			for _, line := range fd.Fix {
				if strings.HasPrefix(line, "#") {
					line = s.paint("2", line)
				}
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
		if fd.Docs != "" {
			fmt.Fprintf(w, "  %s %s\n", s.paint("2", "Docs:"), fd.Docs)
		}
		fmt.Fprintln(w)
	}

	if s.Verbose {
		for _, name := range r.Passed {
			fmt.Fprintf(w, "%s %s\n", s.paint("32", "✓"), name)
		}
		for _, name := range r.Skipped {
			fmt.Fprintf(w, "%s %s\n", s.paint("2", "-"), s.paint("2", name+" (skipped)"))
		}
		fmt.Fprintln(w)
	}

	var fails, warns, infos int
	for _, fd := range r.Findings {
		switch fd.Severity {
		case Fail:
			fails++
		case Warn:
			warns++
		case Info:
			infos++
		}
	}
	parts := []string{s.paint("32", fmt.Sprintf("%d passed", len(r.Passed)))}
	if fails > 0 {
		parts = append(parts, s.paint("31", plural(fails, "problem")))
	}
	if warns > 0 {
		parts = append(parts, s.paint("33", plural(warns, "warning")))
	}
	if infos > 0 {
		parts = append(parts, s.paint("36", plural(infos, "note")))
	}
	if n := len(r.Skipped); n > 0 {
		parts = append(parts, s.paint("2", fmt.Sprintf("%d skipped", n)))
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
	if len(r.Findings) == 0 && len(r.Passed) > 0 {
		fmt.Fprintln(w, "No problems found.")
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// WriteJSON prints the report for scripts and coding agents.
func WriteJSON(w io.Writer, f *Facts, r Result) error {
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Summary string `json:"summary"`
		Result
	}{f.Summary(), r})
}

// HasFailures reports whether any finding is a failure.
func (r Result) HasFailures() bool {
	for _, fd := range r.Findings {
		if fd.Severity == Fail {
			return true
		}
	}
	return false
}
