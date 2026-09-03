package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendWritesExactlyOneCompleteJSONEventPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	event := Event{SchemaVersion: "1", Timestamp: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), Level: "info", Name: "command.completed", Command: "messages get", Outcome: "success", GuildID: "12345678901234567", ChannelID: "23456789012345678", MessageID: "34567890123456789"}
	if err := logger.Append(event); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 1 || !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("log = %q", raw)
	}
	var got Event
	if err := json.Unmarshal(raw[:len(raw)-1], &got); err != nil || got != event {
		t.Fatalf("event = %#v, error = %v", got, err)
	}
}

func TestAppendSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	const count = 100
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := logger.Append(Event{SchemaVersion: "1", Timestamp: time.Now().UTC(), Level: "info", Name: "command.completed", Command: "auth check", Outcome: "success"}); err != nil {
				t.Errorf("Append() error = %v", err)
			}
		}()
	}
	group.Wait()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line %d: %v", lines, err)
		}
	}
	if err := scanner.Err(); err != nil || lines != count {
		t.Fatalf("lines = %d, error = %v", lines, err)
	}
}

func TestOpenAndAppendSurfaceFilesystemFailures(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "audit.jsonl")); err == nil {
		t.Fatal("Open() error = nil")
	}
	logger, err := Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Append(Event{}); err == nil {
		t.Fatal("Append() after Close() error = nil")
	}
}
