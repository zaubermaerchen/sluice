package main

// This file verifies version precedence and development-build filtering.

import (
	"bytes"
	"encoding/json"
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	const tagged = "v1.2.3"
	tests := []struct {
		name     string
		linker   string
		module   string
		settings []debug.BuildSetting
		missing  bool
		want     string
	}{
		{name: "linker wins", linker: "release-custom", module: tagged, want: "release-custom"},
		{name: "linker wins over dirty", linker: tagged, module: "(devel)", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}, want: tagged},
		{name: "empty linker preserved", linker: "", module: tagged, want: ""},
		{name: "tagged", linker: "dev", module: tagged, want: tagged},
		{name: "empty module", linker: "dev", want: "dev"},
		{name: "devel", linker: "dev", module: "(devel)", want: "dev"},
		{name: "pseudo no base", linker: "dev", module: "v0.0.0-20260102030405-abcdef123456", want: "dev"},
		{name: "pseudo post release", linker: "dev", module: "v1.2.4-0.20260102030405-abcdef123456", want: "dev"},
		{name: "pseudo post prerelease", linker: "dev", module: "v1.2.3-rc.1.0.20260102030405-abcdef123456", want: "dev"},
		{name: "pseudo incompatible", linker: "dev", module: "v2.0.0-20260102030405-abcdef123456+incompatible", want: "dev"},
		{name: "dirty", linker: "dev", module: tagged, settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}, want: "dev"},
		{name: "clean", linker: "dev", module: tagged, settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}}, want: tagged},
		{name: "prerelease", linker: "dev", module: "v1.2.3-rc.1", want: "v1.2.3-rc.1"},
		{name: "incompatible", linker: "dev", module: "v2.1.0+incompatible", want: "v2.1.0+incompatible"},
		{name: "prerelease resembling timestamp", linker: "dev", module: "v1.2.3-20260102030405-abcdef123456", want: "v1.2.3-20260102030405-abcdef123456"},
		{name: "short timestamp", linker: "dev", module: "v1.2.3-0.2026010203040-abcdef123456", want: "v1.2.3-0.2026010203040-abcdef123456"},
		{name: "missing build info", linker: "dev", module: tagged, missing: true, want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: tt.module}, Settings: tt.settings}
			if got := resolveVersion(tt.linker, info, !tt.missing); got != tt.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := resolveVersion("dev", nil, true); got != "dev" {
		t.Fatalf("resolveVersion(nil) = %q, want dev", got)
	}
}

func TestVersionCommandsUseResolvedVersion(t *testing.T) {
	previousVersion := version
	version = "v1.2.3-rc.1+custom"
	t.Cleanup(func() { version = previousVersion })
	var versionOutput, describeOutput, diagnostics bytes.Buffer
	if code := runWithIO(&trackingReader{}, &versionOutput, &diagnostics, []string{"--version"}); code != 0 {
		t.Fatalf("--version exit = %d; stderr = %q", code, diagnostics.String())
	}
	if code := runWithIO(&trackingReader{}, &describeOutput, &diagnostics, []string{"--describe"}); code != 0 {
		t.Fatalf("--describe exit = %d; stderr = %q", code, diagnostics.String())
	}
	var document description
	if err := json.Unmarshal(describeOutput.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if got, want := document.Version, currentVersion(); got != want {
		t.Fatalf("description version = %q, want %q", got, want)
	}
	if got, want := versionOutput.String(), "sluice "+document.Version+"\n"; got != want {
		t.Fatalf("--version = %q, want %q", got, want)
	}
}
