package main

// This file exercises process exit codes and output streams of the built CLI.

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCLI_ArgumentContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "sluice")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	buildContext, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	for _, tc := range []struct {
		name   string
		args   []string
		input  string
		code   int
		stdout string
		stderr string
		help   bool
		hint   bool
	}{
		{name: "help", args: []string{"--help"}, help: true},
		{name: "help after error", args: []string{"--unknown", "-h"}, help: true},
		{name: "help after state", args: []string{"closed", "--help"}, help: true},
		{name: "help after terminator", args: []string{"--", "--help"}, help: true},
		{name: "help equals after error", args: []string{"--unknown", "--help=true"}, help: true},
		{name: "help false after state", args: []string{"closed", "--help=false"}, help: true},
		{name: "short help false after terminator", args: []string{"--", "-h=false"}, help: true},
		{name: "legacy help invalid value after error", args: []string{"--unknown", "-help=invalid"}, help: true},
		{name: "help beats invalid configuration", args: []string{"--mode=bad", "--help"}, help: true},
		{name: "unknown long", args: []string{"--unknown"}, code: 2, stderr: "flag provided but not defined: --unknown", hint: true},
		{name: "unknown single dash", args: []string{"-unknown"}, code: 2, stderr: "flag provided but not defined: --unknown", hint: true},
		{name: "missing value", args: []string{"-mode"}, code: 2, stderr: "flag needs an argument: --mode", hint: true},
		{name: "duplicate", args: []string{"-mode=block", "-mode=discard"}, code: 2, stderr: "for flag --mode", hint: true},
		{name: "invalid bool", args: []string{"--version=bad"}, code: 2, stderr: "for --version", hint: true},
		{name: "missing configuration", code: 2, stderr: "--open is required"},
		{name: "invalid configuration", args: []string{"--mode=bad", "--open=duration:1h", "--close=duration:1h", "open"}, code: 2, stderr: "invalid --mode"},
		{name: "startup descriptor", args: []string{"--events-fd=2147483647", "--open=duration:1h", "--close=duration:1h", "open"}, code: 2, stderr: "invalid event file descriptor"},
		{name: "state before options", args: []string{"closed", "--open=duration:1h", "--close=duration:1h"}, code: 2, stderr: "argument order: option --open must precede the initial state"},
		{name: "single dash after state", args: []string{"--open=duration:1h", "--close=duration:1h", "open", "-mode=block"}, code: 2, stderr: "argument order: option --mode must precede the initial state"},
		{name: "single dash compatibility", args: []string{"-mode", "block", "-open", "duration:1h", "-close", "duration:1h", "open"}, input: "forwarded\x00data\n", stdout: "forwarded\x00data\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, tc.args...)
			command.Stdin = strings.NewReader(tc.input)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out: %v", ctx.Err())
			}
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr = %q", code, tc.code, stderr.String())
			}
			if tc.help {
				if !strings.Contains(stdout.String(), "Usage of sluice:") || stderr.Len() != 0 {
					t.Fatalf("help stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			} else {
				if stdout.String() != tc.stdout || !strings.Contains(stderr.String(), tc.stderr) || (tc.stderr == "" && stderr.Len() != 0) || strings.Contains(stderr.String(), "Usage of") {
					t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				if strings.Contains(stderr.String(), "Try 'sluice --help'") != tc.hint {
					t.Fatalf("unexpected help hint: %q", stderr.String())
				}
			}
		})
	}
}
