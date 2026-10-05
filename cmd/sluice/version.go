package main

// This file resolves the reported version from linker and Go build metadata.

import (
	"regexp"
	"runtime/debug"
)

// Match Go's no-base, post-release, and post-prerelease pseudo-version forms,
// so a local development revision is not reported as a tagged release.
var goPseudoVersionPattern = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func currentVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

func resolveVersion(linkerVersion string, info *debug.BuildInfo, ok bool) string {
	if linkerVersion != "dev" {
		return linkerVersion
	}
	if !ok || info == nil {
		return "dev"
	}
	moduleVersion := info.Main.Version
	if moduleVersion == "" || moduleVersion == "(devel)" || goPseudoVersionPattern.MatchString(moduleVersion) {
		return "dev"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return "dev"
		}
	}
	return moduleVersion
}
