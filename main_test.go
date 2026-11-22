package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/supremeinferno/dockdoctor/internal/doctor"
)

func noDocker() doctor.System {
	return doctor.System{
		GOOS: "linux",
		Run: func(context.Context, string, ...string) (string, string, error) {
			return "", "", errors.New("unexpected")
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("no") },
		Exists:   func(string) bool { return false },
	}
}

func TestRunTextExitCode(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, noDocker())
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "docker command is not installed") {
		t.Errorf("output = %q", out.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Error("colors written to a non-terminal")
	}
}

func TestRunJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	run([]string{"-json"}, &out, &errOut, noDocker())
	var report struct {
		Summary  string
		Findings []doctor.Finding
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(report.Findings) != 1 || report.Findings[0].Check != "docker-cli" {
		t.Errorf("findings = %+v", report.Findings)
	}
}

func TestBadFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-nope"}, &out, &errOut, noDocker()); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
