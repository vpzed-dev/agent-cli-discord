package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vpzed-dev/agent-cli-discord/internal/discord"
)

type resultEnvelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

func TestAuthCheckCommandLoadsConfigAndCredential(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me" || r.Header.Get("Authorization") != "Bot test-token" {
			t.Fatalf("request path/auth = %q/%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"agent","global_name":null,"discriminator":"0","avatar":null,"bot":true}`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"auth", "check"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	var identity struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(envelope.Data, &identity); err != nil || identity.Username != "agent" {
		t.Fatalf("data = %s, error = %v", envelope.Data, err)
	}
}

func TestChannelsListCommandReturnsOnlyConfiguredChannels(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/12345678901234567/channels" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":"23456789012345678","type":0,"guild_id":"12345678901234567","name":"agents"},{"id":"99999999999999999","type":0,"guild_id":"12345678901234567","name":"private"}]`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"channels", "list"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	var channels []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &channels); err != nil || len(channels) != 1 || channels[0].ID != "23456789012345678" {
		t.Fatalf("data = %s, error = %v", envelope.Data, err)
	}
}

func TestDiscordCommandFailureUsesStructuredErrorAndNoStdout(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":0,"message":"401: Unauthorized"}`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"auth", "check"}, configDir, server.URL)
	if exitCode == 0 || stdout.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %s", exitCode, stdout.String())
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stderr.Bytes(), &envelope)
	if envelope.Error.Code != "discord.http_error" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestSuccessfulCommandPreservesTokenFilePermissionWarning(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token.env")
	if err := os.WriteFile(tokenPath, []byte("DISCORD_BOT_TOKEN=test-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configDir := writeTestConfig(t, tokenPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"agent","bot":true}`)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exitCode := runWithOptions([]string{"auth", "check"}, &stdout, &stderr, runtimeOptions{
		UserConfigDir: configDir,
		LookupEnv:     func(string) (string, bool) { return "", false },
		Discord:       discord.Options{BaseURL: server.URL},
	})
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	if exitCode != 0 || stderr.Len() != 0 || len(envelope.Warnings) != 1 {
		t.Fatalf("exit = %d, stderr = %q, warnings = %#v", exitCode, stderr.String(), envelope.Warnings)
	}
}

func TestMessagesReadCommandParsesOptionsAndEmitsPage(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channels/23456789012345678/messages" || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("before") != "34567890123456789" {
			t.Fatalf("URL = %s", r.URL.String())
		}
		_, _ = io.WriteString(w, `[{"id":"45678901234567890","channel_id":"23456789012345678","author":{"id":"56789012345678901","username":"agent","bot":true},"content":"hello","timestamp":"2026-09-03T12:00:00Z","attachments":[],"embeds":[],"type":0}]`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"messages", "read", "--channel", "23456789012345678", "--limit", "1", "--before", "34567890123456789"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	var page struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(envelope.Data, &page); err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "45678901234567890" {
		t.Fatalf("data = %s, error = %v", envelope.Data, err)
	}
}

func TestMessageGetCommandEmitsMessage(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channels/23456789012345678/messages/34567890123456789" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"34567890123456789","channel_id":"23456789012345678","author":{"id":"56789012345678901","username":"agent","bot":true},"content":"hello","timestamp":"2026-09-03T12:00:00Z","attachments":[],"embeds":[],"type":0}`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"messages", "get", "--message", "34567890123456789", "--channel", "23456789012345678"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	var message struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &message); err != nil || message.ID != "34567890123456789" {
		t.Fatalf("data = %s, error = %v", envelope.Data, err)
	}
}

func TestMessageArgumentsFailBeforeConfigurationLoading(t *testing.T) {
	tests := [][]string{
		{"messages", "read"},
		{"messages", "read", "--channel", "23456789012345678", "--limit", "not-a-number"},
		{"messages", "read", "--channel", "23456789012345678", "--channel", "23456789012345678"},
		{"messages", "read", "--unknown", "value"},
		{"messages", "get", "--channel", "23456789012345678"},
		{"messages", "get", "--channel"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		exitCode := run(args, &stdout, &stderr)
		if exitCode == 0 || stdout.Len() != 0 {
			t.Fatalf("args = %#v, exit = %d, stdout = %q", args, exitCode, stdout.String())
		}
		var envelope errorEnvelope
		decodeOneJSON(t, stderr.Bytes(), &envelope)
		if envelope.Error.Code != "cli.invalid_arguments" {
			t.Fatalf("args = %#v, error = %#v", args, envelope.Error)
		}
	}
}

func TestMessagesPostReadsContentFromStdin(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content string `json:"content"`
		}
		if r.URL.Path != "/channels/23456789012345678/messages" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Content != "hello from stdin\n" {
			t.Fatalf("path/content = %q/%q", r.URL.Path, payload.Content)
		}
		_, _ = io.WriteString(w, messageResponseForCLI("34567890123456789", payload.Content))
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTestWithInput(t, []string{"messages", "post", "--channel", "23456789012345678"}, configDir, server.URL, strings.NewReader("hello from stdin\n"))
	if exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "34567890123456789") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestMessagesReplyReadsFileAndPassesRepeatedAttachments(t *testing.T) {
	directory := t.TempDir()
	contentPath := filepath.Join(directory, "message.txt")
	firstPath := filepath.Join(directory, "one.txt")
	secondPath := filepath.Join(directory, "two.txt")
	for path, contents := range map[string]string{contentPath: "reply body", firstPath: "one", secondPath: "two"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string][]byte{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			parts[part.FormName()], _ = io.ReadAll(part)
		}
		var payload struct {
			Content   string `json:"content"`
			Reference struct {
				MessageID string `json:"message_id"`
			} `json:"message_reference"`
		}
		if err := json.Unmarshal(parts["payload_json"], &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Content != "reply body" || payload.Reference.MessageID != "34567890123456789" || string(parts["files[0]"]) != "one" || string(parts["files[1]"]) != "two" {
			t.Fatalf("payload/parts = %#v/%#v", payload, parts)
		}
		_, _ = io.WriteString(w, messageResponseForCLI("45678901234567890", payload.Content))
	}))
	defer server.Close()

	args := []string{"messages", "reply", "--channel", "23456789012345678", "--message", "34567890123456789", "--file", contentPath, "--attach", firstPath, "--attach", secondPath}
	_, stderr, exitCode := runForTestWithInput(t, args, configDir, server.URL, strings.NewReader("ignored"))
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestMessageWriteInputErrorsOccurBeforeConfiguration(t *testing.T) {
	tests := []struct {
		args  []string
		input string
	}{
		{[]string{"messages", "post", "--channel", "23456789012345678", "--content", "secret"}, ""},
		{[]string{"messages", "post", "--channel", "23456789012345678", "--file"}, ""},
		{[]string{"messages", "post", "--channel", "23456789012345678", "--file", "/missing"}, ""},
		{[]string{"messages", "post", "--channel", "23456789012345678"}, strings.Repeat("x", maxMessageInputBytes+1)},
	}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		exitCode := runWithOptions(test.args, &stdout, &stderr, runtimeOptions{Input: strings.NewReader(test.input), UserConfigDir: t.TempDir()})
		if exitCode == 0 || stdout.Len() != 0 {
			t.Fatalf("args = %#v, exit = %d", test.args, exitCode)
		}
		var envelope errorEnvelope
		decodeOneJSON(t, stderr.Bytes(), &envelope)
		if envelope.Error.Code != "cli.invalid_arguments" {
			t.Fatalf("error = %#v", envelope.Error)
		}
	}
}

func TestReactionCommandsUseExplicitIdentifiers(t *testing.T) {
	for _, command := range []struct{ action, method string }{{"add", http.MethodPut}, {"remove", http.MethodDelete}} {
		t.Run(command.action, func(t *testing.T) {
			configDir := writeTestConfig(t, "")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != command.method || !strings.HasSuffix(r.URL.EscapedPath(), "/reactions/%F0%9F%94%A5/@me") {
					t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			stdout, stderr, exitCode := runForTest(t, []string{"reactions", command.action, "--channel", "23456789012345678", "--message", "34567890123456789", "--emoji", "🔥"}, configDir, server.URL)
			if exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"emoji":"🔥"`) {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestReactionArgumentsFailBeforeConfiguration(t *testing.T) {
	for _, args := range [][]string{{"reactions", "add"}, {"reactions", "remove", "--channel", "23456789012345678", "--message", "bad", "--emoji", "🔥"}, {"reactions", "delete", "--channel", "23456789012345678"}} {
		var stdout, stderr bytes.Buffer
		if exitCode := run(args, &stdout, &stderr); exitCode == 0 || stdout.Len() != 0 {
			t.Fatalf("args = %#v, exit = %d", args, exitCode)
		}
		var envelope errorEnvelope
		decodeOneJSON(t, stderr.Bytes(), &envelope)
		if envelope.Error.Code != "cli.invalid_arguments" {
			t.Fatalf("error = %#v", envelope.Error)
		}
	}
}

func TestThreadsListCommandUsesGuildActiveEndpoint(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/guilds/12345678901234567/threads/active" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"threads":[{"id":"34567890123456789","guild_id":"12345678901234567","parent_id":"23456789012345678","name":"agent work","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":1440,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}],"members":[]}`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"threads", "list"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	var threads []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &threads); err != nil || len(threads) != 1 || threads[0].ID != "34567890123456789" {
		t.Fatalf("data = %s, error = %v", envelope.Data, err)
	}
}

func TestThreadsCreateCommandDefaultsAutoArchive(t *testing.T) {
	configDir := writeTestConfig(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Name        string `json:"name"`
			Type        int    `json:"type"`
			AutoArchive int    `json:"auto_archive_duration"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/channels/23456789012345678/threads" || json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if payload.Name != "agent work" || payload.Type != 11 || payload.AutoArchive != 1440 {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = io.WriteString(w, `{"id":"34567890123456789","guild_id":"12345678901234567","parent_id":"23456789012345678","name":"agent work","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":1440,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}`)
	}))
	defer server.Close()

	stdout, stderr, exitCode := runForTest(t, []string{"threads", "create", "--channel", "23456789012345678", "--name", "agent work"}, configDir, server.URL)
	if exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"id":"34567890123456789"`) {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestThreadMembershipCommandsFetchMetadataBeforeMutation(t *testing.T) {
	for _, operation := range []struct{ name, method string }{{"join", http.MethodPut}, {"leave", http.MethodDelete}} {
		t.Run(operation.name, func(t *testing.T) {
			configDir := writeTestConfig(t, "")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					if r.Method != http.MethodGet || r.URL.Path != "/channels/34567890123456789" {
						t.Fatalf("metadata request = %s %s", r.Method, r.URL.Path)
					}
					_, _ = io.WriteString(w, `{"id":"34567890123456789","guild_id":"12345678901234567","parent_id":"23456789012345678","name":"agent work","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":1440,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}`)
					return
				}
				if r.Method != operation.method || r.URL.Path != "/channels/34567890123456789/thread-members/@me" {
					t.Fatalf("mutation request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			stdout, stderr, exitCode := runForTest(t, []string{"threads", operation.name, "--thread", "34567890123456789"}, configDir, server.URL)
			if exitCode != 0 || stderr.Len() != 0 || requests != 2 || !strings.Contains(stdout.String(), `"action":"`+operation.name+`"`) {
				t.Fatalf("exit = %d, requests = %d, stdout = %q, stderr = %q", exitCode, requests, stdout.String(), stderr.String())
			}
		})
	}
}

func TestThreadArgumentsFailBeforeConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"threads"},
		{"threads", "list", "extra"},
		{"threads", "create", "--channel", "bad", "--name", "work"},
		{"threads", "create", "--channel", "23456789012345678", "--name", "work", "--auto-archive", "61"},
		{"threads", "join", "--thread", "bad"},
		{"threads", "leave"},
	} {
		var stdout, stderr bytes.Buffer
		if exitCode := run(args, &stdout, &stderr); exitCode == 0 || stdout.Len() != 0 {
			t.Fatalf("args = %#v, exit = %d", args, exitCode)
		}
		var envelope errorEnvelope
		decodeOneJSON(t, stderr.Bytes(), &envelope)
		if envelope.Error.Code != "cli.invalid_arguments" {
			t.Fatalf("args = %#v, error = %#v", args, envelope.Error)
		}
	}
}

func TestThreadCreateRejectsExplicitRestrictionsWithoutNetwork(t *testing.T) {
	configDir := writeTestConfigWithThreads(t, []string{"34567890123456789"})
	stdout, stderr, exitCode := runForTest(t, []string{"threads", "create", "--channel", "23456789012345678", "--name", "work"}, configDir, "http://127.0.0.1:1")
	if exitCode == 0 || stdout.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q", exitCode, stdout.String())
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stderr.Bytes(), &envelope)
	if envelope.Error.Code != "policy.thread_creation_restricted" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestAuditLoggingDefaultsOffAndEnabledLogContainsOnlySafeFields(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		configDir := writeTestConfig(t, "")
		logPath := filepath.Join(configDir, "unexpected.jsonl")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"agent","bot":true}`)
		}))
		defer server.Close()
		_, stderr, exitCode := runForTest(t, []string{"auth", "check"}, configDir, server.URL)
		if exitCode != 0 || stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
		}
		if _, err := os.Stat(logPath); !os.IsNotExist(err) {
			t.Fatalf("unexpected log file error = %v", err)
		}
	})

	t.Run("enabled", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "audit.jsonl")
		configDir := writeTestConfigWithLog(t, logPath)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, messageResponseForCLI("34567890123456789", "private message body"))
		}))
		defer server.Close()
		_, stderr, exitCode := runForTestWithInput(t, []string{"messages", "post", "--channel", "23456789012345678"}, configDir, server.URL, strings.NewReader("private message body"))
		if exitCode != 0 || stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
		}
		raw, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private message body") || strings.Contains(string(raw), "test-token") || strings.Contains(string(raw), "Authorization") {
			t.Fatalf("audit log disclosed sensitive data: %q", raw)
		}
		var event struct {
			SchemaVersion string `json:"schema_version"`
			Timestamp     string `json:"timestamp"`
			Level         string `json:"level"`
			Name          string `json:"event"`
			Command       string `json:"command"`
			Outcome       string `json:"outcome"`
			GuildID       string `json:"guild_id"`
			ChannelID     string `json:"channel_id"`
		}
		decodeOneJSON(t, raw, &event)
		if event.SchemaVersion != "1" || event.Level != "info" || event.Name != "command.completed" || event.Command != "messages post" || event.Outcome != "success" || event.GuildID != "12345678901234567" || event.ChannelID != "23456789012345678" {
			t.Fatalf("event = %#v", event)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
			t.Fatalf("timestamp = %q: %v", event.Timestamp, err)
		}
	})
}

func TestAuditOpenFailurePreventsCommandNetworkAccess(t *testing.T) {
	configDir := writeTestConfigWithLog(t, filepath.Join(t.TempDir(), "missing", "audit.jsonl"))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"agent","bot":true}`)
	}))
	defer server.Close()
	stdout, stderr, exitCode := runForTest(t, []string{"auth", "check"}, configDir, server.URL)
	if exitCode == 0 || stdout.Len() != 0 || requests != 0 {
		t.Fatalf("exit = %d, stdout = %q, requests = %d", exitCode, stdout.String(), requests)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stderr.Bytes(), &envelope)
	if envelope.Error.Code != "log.unavailable" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func runForTestWithInput(t *testing.T, args []string, configDir, baseURL string, input io.Reader) (bytes.Buffer, bytes.Buffer, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := runWithOptions(args, &stdout, &stderr, runtimeOptions{UserConfigDir: configDir, Input: input, LookupEnv: func(string) (string, bool) { return "test-token", true }, Discord: discord.Options{BaseURL: baseURL}})
	return stdout, stderr, exitCode
}

func messageResponseForCLI(id, content string) string {
	raw, _ := json.Marshal(map[string]any{"id": id, "channel_id": "23456789012345678", "author": map[string]any{"id": "56789012345678901", "username": "agent", "bot": true}, "content": content, "timestamp": "2026-09-03T12:00:00Z", "attachments": []any{}, "embeds": []any{}, "type": 0})
	return string(raw)
}

func runForTest(t *testing.T, args []string, configDir, baseURL string) (bytes.Buffer, bytes.Buffer, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := runWithOptions(args, &stdout, &stderr, runtimeOptions{
		UserConfigDir: configDir,
		LookupEnv: func(key string) (string, bool) {
			if key != "DISCORD_BOT_TOKEN" {
				t.Fatalf("environment key = %q", key)
			}
			return "test-token", true
		},
		Discord: discord.Options{BaseURL: baseURL},
	})
	return stdout, stderr, exitCode
}

func writeTestConfig(t *testing.T, tokenFile string) string {
	t.Helper()
	base := t.TempDir()
	directory := filepath.Join(base, "agent-cli-discord")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":"1","guild_id":"12345678901234567","allowed_channel_ids":["23456789012345678"],"request_timeout":"1s","command_timeout":"2s"`
	if tokenFile != "" {
		raw += `,"token_file":` + string(mustJSON(t, tokenFile))
	}
	raw += `}`
	if err := os.WriteFile(filepath.Join(directory, "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

func writeTestConfigWithThreads(t *testing.T, threadIDs []string) string {
	t.Helper()
	base := writeTestConfig(t, "")
	path := filepath.Join(base, "agent-cli-discord", "config.json")
	raw := `{"schema_version":"1","guild_id":"12345678901234567","allowed_channel_ids":["23456789012345678"],"allowed_thread_ids":` + string(mustJSON(t, threadIDs)) + `,"request_timeout":"1s","command_timeout":"2s"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

func writeTestConfigWithLog(t *testing.T, logPath string) string {
	t.Helper()
	base := writeTestConfig(t, "")
	path := filepath.Join(base, "agent-cli-discord", "config.json")
	raw := `{"schema_version":"1","guild_id":"12345678901234567","allowed_channel_ids":["23456789012345678"],"request_timeout":"1s","command_timeout":"2s","log":{"path":` + string(mustJSON(t, logPath)) + `,"level":"info"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type errorEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

func decodeOneJSON(t *testing.T, raw []byte, dst any) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(dst); err != nil {
		t.Fatalf("decode output: %v\noutput: %q", err, raw)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("output contains more than one JSON value: %q", raw)
	}
}

func TestVersionProducesOneJSONDocument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var envelope resultEnvelope
	decodeOneJSON(t, stdout.Bytes(), &envelope)
	if !envelope.OK {
		t.Fatal("ok = false, want true")
	}

	var data struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.Name != "agent-cli-discord" {
		t.Fatalf("name = %q, want agent-cli-discord", data.Name)
	}
	if data.Version == "" {
		t.Fatal("version must be a non-empty JSON string")
	}
	if data.SchemaVersion != "1" {
		t.Fatalf("schema_version = %q, want string %q", data.SchemaVersion, "1")
	}
}

func TestUnknownCommandProducesStructuredErrorOnlyOnStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"not-a-command"}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("exit code = 0, want nonzero")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	var envelope errorEnvelope
	decodeOneJSON(t, stderr.Bytes(), &envelope)
	if envelope.OK {
		t.Fatal("ok = true, want false")
	}
	if envelope.Error.Code != "cli.unknown_command" {
		t.Fatalf("error code = %q, want cli.unknown_command", envelope.Error.Code)
	}
	if envelope.Error.Message == "" {
		t.Fatal("error message must not be empty")
	}
	if envelope.Error.Retryable {
		t.Fatal("unknown-command error must not be retryable")
	}
}

func TestMalformedArgumentsProduceStructuredError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"version", "unexpected"}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("exit code = 0, want nonzero")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	var envelope errorEnvelope
	decodeOneJSON(t, stderr.Bytes(), &envelope)
	if envelope.Error.Code != "cli.invalid_arguments" {
		t.Fatalf("error code = %q, want cli.invalid_arguments", envelope.Error.Code)
	}
}
