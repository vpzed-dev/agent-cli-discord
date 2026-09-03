package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	testGuild   = "12345678901234567"
	testChannel = "23456789012345678"
	testThread  = "34567890123456789"
)

func TestDecodeValidStrictJSON(t *testing.T) {
	raw := `{
  "schema_version": "1",
  "guild_id": "` + testGuild + `",
  "allowed_channel_ids": ["` + testChannel + `"],
  "allowed_thread_ids": ["` + testThread + `"],
  "request_timeout": "10s",
  "command_timeout": "30s",
  "token_file": "/run/secrets/discord.env",
  "log": {"path": "/var/log/agent-cli-discord.jsonl", "level": "info"}
}`

	got, err := Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.SchemaVersion != "1" {
		t.Fatalf("SchemaVersion = %q, want 1", got.SchemaVersion)
	}
	if got.GuildID != testGuild {
		t.Fatalf("GuildID = %q, want %q", got.GuildID, testGuild)
	}
	if len(got.AllowedChannelIDs) != 1 || got.AllowedChannelIDs[0] != testChannel {
		t.Fatalf("AllowedChannelIDs = %#v", got.AllowedChannelIDs)
	}
	if len(got.AllowedThreadIDs) != 1 || got.AllowedThreadIDs[0] != testThread {
		t.Fatalf("AllowedThreadIDs = %#v", got.AllowedThreadIDs)
	}
	if got.RequestTimeout != 10*time.Second {
		t.Fatalf("RequestTimeout = %s, want 10s", got.RequestTimeout)
	}
	if got.CommandTimeout != 30*time.Second {
		t.Fatalf("CommandTimeout = %s, want 30s", got.CommandTimeout)
	}
	if got.TokenFile != "/run/secrets/discord.env" {
		t.Fatalf("TokenFile = %q", got.TokenFile)
	}
	if got.Log == nil || got.Log.Path != "/var/log/agent-cli-discord.jsonl" || got.Log.Level != "info" {
		t.Fatalf("Log = %#v", got.Log)
	}
}

func TestDecodeAppliesSafeDefaults(t *testing.T) {
	raw := `{
  "schema_version": "1",
  "guild_id": "` + testGuild + `",
  "allowed_channel_ids": ["` + testChannel + `"]
}`

	got, err := Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.RequestTimeout != 15*time.Second {
		t.Fatalf("RequestTimeout = %s, want 15s", got.RequestTimeout)
	}
	if got.CommandTimeout != 30*time.Second {
		t.Fatalf("CommandTimeout = %s, want 30s", got.CommandTimeout)
	}
	if got.Log != nil {
		t.Fatalf("Log = %#v, want nil because logging defaults off", got.Log)
	}
}

func TestDecodeRejectsNonStrictJSON(t *testing.T) {
	tests := map[string]string{
		"line comment":   `{"schema_version":"1", // no comments\n "guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`,
		"block comment":  `{"schema_version":"1", /* no comments */ "guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`,
		"trailing comma": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],}`,
		"trailing value": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]} {}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil, want rejection")
			}
		})
	}
}

func TestDecodeAllowsCommentMarkersInsideStrings(t *testing.T) {
	raw := `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"token_file":"/safe//name/*literal*/.env"}`

	got, err := Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.TokenFile != "/safe//name/*literal*/.env" {
		t.Fatalf("TokenFile = %q", got.TokenFile)
	}
}

func TestDecodeRejectsUnknownAndDuplicateFields(t *testing.T) {
	tests := map[string]string{
		"unknown top-level field": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"surprise":true}`,
		"unknown nested field":    `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"log":{"path":"audit.jsonl","level":"info","surprise":true}}`,
		"duplicate top-level":     `{"schema_version":"1","schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`,
		"duplicate nested":        `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"log":{"path":"one.jsonl","path":"two.jsonl","level":"info"}}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil, want rejection")
			}
		})
	}
}

func TestDecodeValidatesSchemaAndRequiredFields(t *testing.T) {
	tests := map[string]string{
		"unsupported schema": `{"schema_version":"2","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`,
		"missing schema":     `{"guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`,
		"missing guild":      `{"schema_version":"1","allowed_channel_ids":["` + testChannel + `"]}`,
		"missing channels":   `{"schema_version":"1","guild_id":"` + testGuild + `"}`,
		"empty channels":     `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":[]}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil, want rejection")
			}
		})
	}
}

func TestDecodeValidatesSnowflakesAndUniqueness(t *testing.T) {
	tests := map[string]string{
		"short guild":       `{"schema_version":"1","guild_id":"123","allowed_channel_ids":["` + testChannel + `"]}`,
		"non-digit channel": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["not-an-id"]}`,
		"duplicate channel": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `","` + testChannel + `"]}`,
		"duplicate thread":  `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"allowed_thread_ids":["` + testThread + `","` + testThread + `"]}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil, want rejection")
			}
		})
	}
}

func TestDecodeValidatesTimeoutsAndLogSettings(t *testing.T) {
	tests := map[string]string{
		"zero request timeout":  `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"request_timeout":"0s"}`,
		"bad command timeout":   `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"command_timeout":"later"}`,
		"command too short":     `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"request_timeout":"20s","command_timeout":"10s"}`,
		"log missing path":      `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"log":{"level":"info"}}`,
		"unsupported log level": `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"],"log":{"path":"audit.jsonl","level":"verbose"}}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("Decode() error = nil, want rejection")
			}
		})
	}
}

func TestEncodeProducesDeterministicStrictJSON(t *testing.T) {
	input := Config{
		SchemaVersion:     "1",
		GuildID:           testGuild,
		AllowedChannelIDs: []string{testChannel},
		RequestTimeout:    10 * time.Second,
		CommandTimeout:    30 * time.Second,
	}

	var first bytes.Buffer
	if err := Encode(&first, input); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	var second bytes.Buffer
	if err := Encode(&second, input); err != nil {
		t.Fatalf("second Encode() error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("encoding is not deterministic:\nfirst:  %q\nsecond: %q", first.String(), second.String())
	}
	if strings.Contains(first.String(), "//") || strings.Contains(first.String(), "/*") {
		t.Fatalf("encoded configuration contains comment syntax: %q", first.String())
	}

	got, err := Decode(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatalf("Decode(Encode()) error = %v", err)
	}
	if got.GuildID != input.GuildID || got.RequestTimeout != input.RequestTimeout {
		t.Fatalf("round trip = %#v, want %#v", got, input)
	}
}

func TestPathUsesPlatformConfigurationDirectory(t *testing.T) {
	base := filepath.Join("base", "config")
	want := filepath.Join(base, "agent-cli-discord", "config.json")
	if got := Path(base); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestLoadReadsConfigurationFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.GuildID != testGuild {
		t.Fatalf("GuildID = %q, want %q", got.GuildID, testGuild)
	}
}

func TestPublishedConfigurationExamplesRemainValid(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "examples", "config.example.json"),
		filepath.Join("..", "..", "examples", "live-test", "config.example.json"),
	} {
		t.Run(path, func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := Decode(file); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
		})
	}
}

func TestLoadRejectsWritableByOtherUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"schema_version":"1","guild_id":"` + testGuild + `","allowed_channel_ids":["` + testChannel + `"]}`
	if err := os.WriteFile(path, []byte(raw), 0o622); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want unsafe-permission rejection")
	}
}
