package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var settingsAuditMu sync.Mutex

type settingsAuditEvent struct {
	TS       string `json:"ts"`
	ChatID   int64  `json:"chat_id"`
	UserID   int64  `json:"user_id"`
	Username string `json:"username,omitempty"`
	Setting  string `json:"setting"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

func settingsAuditPath(state *MonitorState) string {
	if state == nil {
		return "./data/settings_audit.log"
	}
	state.mu.Lock()
	sf := strings.TrimSpace(state.stateFile)
	state.mu.Unlock()
	if sf == "" {
		return "./data/settings_audit.log"
	}
	return filepath.Join(filepath.Dir(sf), "settings_audit.log")
}

func appendSettingsAudit(state *MonitorState, ev settingsAuditEvent) {
	if strings.TrimSpace(ev.Setting) == "" {
		return
	}
	settingsAuditMu.Lock()
	defer settingsAuditMu.Unlock()

	path := settingsAuditPath(state)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logIfErr("settings_audit.mkdir", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		logIfErr("settings_audit.open", err)
		return
	}
	defer f.Close()
	if ev.TS == "" {
		ev.TS = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		logIfErr("settings_audit.marshal", err)
		return
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		logIfErr("settings_audit.write", err)
	}
}

func actorFromUpdate(upd tgUpdate) (chatID int64, userID int64, username string) {
	if upd.CallbackQuery != nil {
		if upd.CallbackQuery.Message != nil {
			chatID = upd.CallbackQuery.Message.Chat.ID
		}
		if upd.CallbackQuery.From != nil {
			userID = upd.CallbackQuery.From.ID
			username = strings.TrimSpace(upd.CallbackQuery.From.Username)
		}
		return
	}
	if upd.Message != nil {
		chatID = upd.Message.Chat.ID
		if upd.Message.From != nil {
			userID = upd.Message.From.ID
			username = strings.TrimSpace(upd.Message.From.Username)
		}
	}
	return
}
