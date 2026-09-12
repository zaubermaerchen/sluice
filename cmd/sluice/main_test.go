package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_Default(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, nil)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if got := stdout.String(); got != "Hello World\n" {
		t.Fatalf("expected %q, got %q", "Hello World\n", got)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRun_Version(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, []string{"--version"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if got := stdout.String(); got != "sluice "+version+"\n" {
		t.Fatalf("expected %q, got %q", "sluice "+version+"\n", got)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := run(&stdout, &stderr, args)

			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}

			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}

			got := stderr.String()
			if !strings.Contains(got, "Usage of sluice:") {
				t.Fatalf("expected usage in stderr, got %q", got)
			}

			if !strings.Contains(got, "-version") {
				t.Fatalf("expected version flag in usage, got %q", got)
			}
		})
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, []string{"--unknown"})

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "flag provided but not defined: -unknown") {
		t.Fatalf("expected invalid flag error in stderr, got %q", got)
	}

	if !strings.Contains(got, "Usage of sluice:") {
		t.Fatalf("expected usage in stderr, got %q", got)
	}
}

func TestRun_HelpWithInvalidFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, []string{"--help", "--unknown"})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "Usage of sluice:") {
		t.Fatalf("expected usage in stderr, got %q", got)
	}

	if strings.Contains(got, "flag provided but not defined") {
		t.Fatalf("did not expect invalid flag error when help is requested, got %q", got)
	}
}
