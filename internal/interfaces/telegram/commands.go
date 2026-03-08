package telegram

import "strings"

func NormalizeCommand(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "status", "/status":
		return "/status"
	case "daily report", "/daily":
		return "/daily"
	case "menu", "/menu", "/start":
		return "/menu"
	case "help", "/help":
		return "/help"
	case "version", "/version":
		return "/version"
	default:
		return s
	}
}
