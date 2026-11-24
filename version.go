package main

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

// Set at build time with:
// -ldflags "-X main.appVersion=... -X main.appCommit=... -X main.appDate=..."
// See the Makefile. Empty means plain go build/test, which falls back to
// the VCS stamp the go tool embeds, then to "dev".
var (
	appVersion = ""
	appCommit  = ""
	appDate    = ""
)

type buildInfo struct {
	version string
	commit  string
	date    string
	dirty   bool
}

func readBuildInfo() buildInfo {
	if appVersion != "" || appCommit != "" {
		return buildInfo{
			version: strings.TrimSpace(appVersion),
			commit:  strings.TrimSpace(appCommit),
			date:    strings.TrimSpace(appDate),
		}
	}
	var bi buildInfo
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				bi.commit = shortHash(strings.TrimSpace(s.Value))
			case "vcs.time":
				if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s.Value)); err == nil {
					bi.date = t.Format("2006-01-02")
				}
			case "vcs.modified":
				bi.dirty = strings.TrimSpace(s.Value) == "true"
			}
		}
	}
	return bi
}

func shortHash(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// versionString renders "llog <version>" plus commit and date when known,
// e.g. "llog v0.1.0 (6ce796f 2026-11-24)". Dirty trees get a +dirty marker.
func versionString() string {
	bi := readBuildInfo()
	version := bi.version
	if version == "" {
		version = "dev"
	}
	var extra []string
	commit := bi.commit
	if commit != "" && bi.dirty {
		commit += "+dirty"
	}
	if commit != "" {
		extra = append(extra, commit)
	}
	if bi.date != "" {
		extra = append(extra, bi.date)
	}
	if len(extra) == 0 {
		return fmt.Sprintf("llog %s", version)
	}
	return fmt.Sprintf("llog %s (%s)", version, strings.Join(extra, " "))
}
