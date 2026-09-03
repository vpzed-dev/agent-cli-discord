package audit

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

type Event struct {
	SchemaVersion string    `json:"schema_version"`
	Timestamp     time.Time `json:"timestamp"`
	Level         string    `json:"level"`
	Name          string    `json:"event"`
	Command       string    `json:"command"`
	Outcome       string    `json:"outcome"`
	GuildID       string    `json:"guild_id,omitempty"`
	ChannelID     string    `json:"channel_id,omitempty"`
	MessageID     string    `json:"message_id,omitempty"`
	ThreadID      string    `json:"thread_id,omitempty"`
}

type Logger struct {
	file *os.File
	mu   sync.Mutex
}

func Open(path string) (*Logger, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("audit destination must be a regular file")
	}
	return &Logger{file: file}, nil
}

func (l *Logger) Append(event Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	written, err := l.file.Write(raw)
	if err != nil {
		return err
	}
	if written != len(raw) {
		return errors.New("incomplete audit write")
	}
	return nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
