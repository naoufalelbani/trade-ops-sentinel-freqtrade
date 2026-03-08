package main

import (
	"os"
	"path/filepath"
	"strings"
)

func currentReleaseTag() string {
	v := strings.TrimSpace(appVersion)
	v = strings.TrimPrefix(strings.ToLower(v), "v")
	return v
}

func loadReleaseNotesForCurrentTag(maxItems int) []string {
	if maxItems <= 0 {
		maxItems = 3
	}
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		raw, err = os.ReadFile(filepath.Join(".", "CHANGELOG.md"))
		if err != nil {
			return nil
		}
	}
	lines := strings.Split(string(raw), "\n")
	tag := currentReleaseTag()
	if tag == "" || tag == "dev" {
		return nil
	}
	section := "## [" + tag + "]"
	start := -1
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(section)) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	out := make([]string, 0, maxItems)
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## [") {
			break
		}
		if strings.HasPrefix(line, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if item != "" {
				out = append(out, item)
				if len(out) >= maxItems {
					break
				}
			}
		}
	}
	return out
}

func releaseNotesInline(maxItems int) string {
	items := loadReleaseNotesForCurrentTag(maxItems)
	if len(items) == 0 {
		return "n/a"
	}
	return strings.Join(items, " | ")
}
