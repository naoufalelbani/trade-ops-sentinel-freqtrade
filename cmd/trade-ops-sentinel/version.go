package main

import (
	"fmt"
	"strings"
)

var (
	appVersion   = "v0.3.0"
	appCommit    = "none"
	appBuildDate = "unknown"
)

func versionSummary() string {
	return fmt.Sprintf("%s (commit=%s built=%s)", appVersion, appCommit, appBuildDate)
}

func versionReport() string {
	releaseTag := strings.TrimSpace(appVersion)
	if releaseTag == "" {
		releaseTag = "dev"
	}
	notes := loadReleaseNotesForCurrentTag(5)
	notesText := "n/a"
	if len(notes) > 0 {
		notesText = strings.Join(notes, "\n- ")
		notesText = "- " + notesText
	}
	return fmt.Sprintf(
		"Trade Ops Sentinel version\nVersion=%s\nCommit=%s\nBuild date=%s\nRelease=%s\nChangelog:\n%s",
		releaseTag,
		appCommit,
		appBuildDate,
		releaseTag,
		notesText,
	)
}
