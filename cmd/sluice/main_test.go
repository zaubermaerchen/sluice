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

	if got := stdout.String(); got != helloMessage+"\n" {
		t.Fatalf("expected %q, got %q", helloMessage+"\n", got)
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

	if got := stdout.String(); got != versionMessage+"\n" {
		t.Fatalf("expected %q, got %q", versionMessage+"\n", got)
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

			got := stdout.String()
			if !strings.Contains(got, "Usage of sluice:") {
				t.Fatalf("expected usage in stdout, got %q", got)
			}

			if !strings.Contains(got, "-version") {
				t.Fatalf("expected version flag in usage, got %q", got)
			}

			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}
