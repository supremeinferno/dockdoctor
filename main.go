// Command dockdoctor finds the common reasons a Docker or Docker Compose
// setup doesn't work, and prints the fix for each.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/supremeinferno/dockdoctor/internal/doctor"
)

var version = "dev"

type fileList []string

func (l *fileList) String() string     { return strings.Join(*l, ",") }
func (l *fileList) Set(v string) error { *l = append(*l, v); return nil }

func run(args []string, stdout, stderr io.Writer, sys doctor.System) int {
	fs := flag.NewFlagSet("dockdoctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var files fileList
	fs.Var(&files, "f", "Compose file to check (repeatable; default: the one in the current directory)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("v", false, "also list the checks that passed or were skipped")
	noColor := fs.Bool("no-color", false, "don't use colors")
	showVersion := fs.Bool("version", false, "print the version")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: dockdoctor [-f compose.yaml] [-json] [-v]")
		fmt.Fprintln(stderr, "\nFinds the common reasons a Docker or Compose setup doesn't work, and prints the fix.")
		fmt.Fprintln(stderr, "It only reads: it never starts, stops or changes anything.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "dockdoctor", version)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	facts := doctor.Gather(ctx, sys, files)
	result := doctor.Run(facts)

	if *asJSON {
		if err := doctor.WriteJSON(stdout, facts, result); err != nil {
			fmt.Fprintln(stderr, "dockdoctor:", err)
			return 2
		}
	} else {
		doctor.WriteText(stdout, facts, result, doctor.Style{
			Color:   !*noColor && os.Getenv("NO_COLOR") == "" && isTerminal(stdout),
			Verbose: *verbose,
		})
	}
	if result.HasFailures() {
		return 1
	}
	return 0
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, doctor.RealSystem()))
}
