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
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/discord"
)

func TestHelpForms(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"", []string{"help [PATH]", "PATH --help", "PATH -h", "config.json", "DISCORD_BOT_TOKEN", "15s", "30s", "stdout", "stderr"}},
		{"version", []string{"schema_version", "offline"}},
		{"auth", []string{"auth check"}},
		{"auth check", []string{"bot", "username"}},
		{"channels", []string{"channels list"}},
		{"channels list", []string{"array", "guild"}},
		{"messages", []string{"messages read", "messages get", "messages post", "messages reply"}},
		{"messages read", []string{"--channel ID", "--limit N", "50", "1 to 100", "--before ID", "--after ID", "--around ID", "mutually exclusive", "oldest", "cursor.before", "cursor.after"}},
		{"messages get", []string{"--channel ID", "--message ID", "message object"}},
		{"messages post", []string{"--channel ID", "--file PATH", "--attach PATH", "stdin", "EOF", "8000", "2000", "10 MiB", "24 MiB", "outcome_unknown"}},
		{"messages reply", []string{"--channel ID", "--message ID", "--file PATH", "--attach PATH", "stdin", "EOF", "8000", "2000", "10 MiB", "24 MiB", "outcome_unknown"}},
		{"reactions", []string{"reactions add", "reactions remove"}},
		{"reactions add", []string{"--channel ID", "--message ID", "--emoji EMOJI", "Unicode", "name:id", "action"}},
		{"reactions remove", []string{"--channel ID", "--message ID", "--emoji EMOJI", "bot", "action"}},
		{"threads", []string{"threads list", "threads create", "threads join", "threads leave"}},
		{"threads list", []string{"active", "array"}},
		{"threads create", []string{"--channel PARENT_ID", "--name NAME", "--auto-archive MINUTES", "1 to 100", "1440", "60", "4320", "10080", "public", "outcome_unknown"}},
		{"threads join", []string{"--thread ID", "archived", "thread_id"}},
		{"threads leave", []string{"--thread ID", "archived", "thread_id"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			path := strings.Fields(test.path)
			forms := [][]string{append([]string{"help"}, path...), append(slices.Clone(path), "--help"), append(slices.Clone(path), "-h")}
			var first string
			for _, args := range forms {
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					original := slices.Clone(args)
					var stdout, stderr bytes.Buffer
					code := runWithOptions(args, &stdout, &stderr, runtimeOptions{
						UserConfigDir: t.TempDir(), Input: helpPanicReader{},
						LookupEnv: func(string) (string, bool) { panic("help read credentials") },
					})
					if code != 0 || stderr.Len() != 0 {
						t.Fatalf("exit = %d, stderr = %q; want successful offline help", code, stderr.String())
					}
					text := stdout.String()
					for _, want := range append([]string{"Usage:\n  agent-cli-discord", "Examples:", "agent-cli-discord " + test.path}, test.want...) {
						if !strings.Contains(text, want) {
							t.Errorf("help is missing %q:\n%s", want, text)
						}
					}
					if !strings.HasSuffix(text, "\n") || json.Valid(stdout.Bytes()) || strings.Contains(text, "\x1b") {
						t.Errorf("help must be newline-terminated plain text: %q", text)
					}
					if first == "" {
						first = text
					} else if text != first {
						t.Error("help aliases produced different output")
					}
					if !slices.Equal(args, original) {
						t.Fatalf("help changed argv: %q", args)
					}
				})
			}
		})
	}
}

func TestHelpRootDiscoversEveryCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	text := stdout.String()
	previous := -1
	for _, command := range []string{
		"version", "auth check", "channels list", "messages read", "messages get", "messages post", "messages reply",
		"reactions add", "reactions remove", "threads list", "threads create", "threads join", "threads leave",
	} {
		position := strings.Index(text, "\n  "+command+"  ")
		if position <= previous {
			t.Fatalf("missing or out-of-order command %q:\n%s", command, text)
		}
		previous = position
	}
}

func TestHelpRejectsMalformedForms(t *testing.T) {
	for _, args := range [][]string{
		{"help", "missing"}, {"help", "messages", "missing"}, {"help", "messages read"},
		{"help", "help"}, {"help", "--help"}, {"help", "-h"}, {"--help", "messages"}, {"-h", "messages"},
		{"help", "messages", "read", "--channel", "23456789012345678"},
		{"messages", "--help", "read"}, {"messages", "read", "--help", "extra"}, {"threads", "create", "-h", "--name", "work"},
		{"messages", "read", "--channel", "23456789012345678", "--help"},
		{"messages", "post", "--help", "--file", "missing"},
		{"messages", "post", "--channel", "23456789012345678", "--file", "--help"},
		{"threads", "create", "--channel", "23456789012345678", "--name", "--help"},
		{"messages", "missing", "--help"}, {"messages", "read", "help"}, {"version", "help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithOptions(args, &stdout, &stderr, runtimeOptions{
				UserConfigDir: t.TempDir(), Input: helpPanicReader{},
				LookupEnv: func(string) (string, bool) { panic("invalid help read credentials") },
			})
			var result failureEnvelope
			decodeOneJSON(t, stderr.Bytes(), &result)
			if code != 2 || stdout.Len() != 0 || result.OK || result.Error.Code != "cli.invalid_arguments" || result.Error.Retryable {
				t.Fatalf("exit = %d, stdout = %q, error = %#v", code, stdout.String(), result)
			}
		})
	}
}

func TestHelpPreservesOperationalErrors(t *testing.T) {
	for _, test := range []struct {
		args          []string
		code, message string
	}{
		{nil, "cli.invalid_arguments", "a command is required; use --help for usage"},
		{[]string{"unknown", "--help"}, "cli.unknown_command", "unknown command: unknown"},
		{[]string{"messages"}, "cli.invalid_arguments", "usage: agent-cli-discord messages read|get|post|reply [options]"},
		{[]string{"messages", "read", "--channel"}, "cli.invalid_arguments", "every option requires a value"},
		{[]string{"messages", "read", "--channel", "-h"}, "cli.invalid_arguments", "--channel must be a Discord snowflake"},
		{[]string{"messages", "read", "--channel", "23456789012345678", "--limit", "help"}, "cli.invalid_arguments", "--limit must be an integer between 1 and 100"},
		{[]string{"messages", "post", "--channel", "23456789012345678", "--file", "--help"}, "cli.invalid_arguments", "option \"--file\" requires a value"},
		{[]string{"version", "extra"}, "cli.invalid_arguments", "version accepts no arguments"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(test.args, &stdout, &stderr)
			var result failureEnvelope
			decodeOneJSON(t, stderr.Bytes(), &result)
			if code != 2 || stdout.Len() != 0 || result.OK || result.Error.Code != test.code || result.Error.Message != test.message || result.Error.Retryable {
				t.Fatalf("exit = %d, stdout = %q, error = %#v", code, stdout.String(), result)
			}
		})
	}
}

func TestHelpLeavesFilesAndNetworkUntouched(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, state := range []string{"missing config", "invalid config", "new audit", "existing audit"} {
		t.Run(state, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "audit.jsonl")
			configDir := writeTestConfigWithLog(t, logPath)
			configPath := filepath.Join(configDir, "agent-cli-discord", "config.json")
			if state == "missing config" {
				if err := os.Remove(configPath); err != nil {
					t.Fatal(err)
				}
			}
			if state == "invalid config" {
				if err := os.WriteFile(configPath, []byte("invalid config sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if state == "existing audit" {
				if err := os.WriteFile(logPath, []byte("audit sentinel\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tokenPath := filepath.Join(configDir, "agent-cli-discord", "token.env")
			if err := os.WriteFile(tokenPath, []byte("invalid token sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
			configBefore, _ := os.ReadFile(configPath)
			for _, args := range [][]string{{"--help"}, {"auth", "check", "-h"}, {"messages", "post", "--help"}, {"help", "messages", "reply"}, {"threads", "create", "--help"}} {
				var stdout, stderr bytes.Buffer
				code := runWithOptions(args, &stdout, &stderr, runtimeOptions{
					UserConfigDir: configDir, Input: helpPanicReader{},
					LookupEnv: func(string) (string, bool) { panic("help read credentials") },
					Discord:   discord.Options{BaseURL: server.URL},
				})
				if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Usage:\n  agent-cli-discord") {
					t.Fatalf("args = %q, exit = %d, stdout = %q, stderr = %q", args, code, stdout.String(), stderr.String())
				}
			}
			configAfter, configErr := os.ReadFile(configPath)
			if !bytes.Equal(configAfter, configBefore) || (state == "missing config" && !os.IsNotExist(configErr)) {
				t.Fatalf("config changed: %q, error = %v", configAfter, configErr)
			}
			token, err := os.ReadFile(tokenPath)
			if err != nil || string(token) != "invalid token sentinel" {
				t.Fatalf("token changed: %q, %v", token, err)
			}
			log, err := os.ReadFile(logPath)
			if state == "existing audit" {
				if err != nil || string(log) != "audit sentinel\n" {
					t.Fatalf("audit changed: %q, %v", log, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("help created audit log: %q, %v", log, err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("help made %d HTTP requests", requests.Load())
	}
}

func TestHelpLookingThreadNamesStayOperational(t *testing.T) {
	for _, name := range []string{"help", "-h"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Name string `json:"name"`
				}
				if r.Method != http.MethodPost || r.URL.Path != "/channels/23456789012345678/threads" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Name != name {
					t.Errorf("request = %s %s, payload = %#v", r.Method, r.URL.Path, payload)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "34567890123456789", "guild_id": "12345678901234567", "parent_id": "23456789012345678", "name": payload.Name})
			}))
			defer server.Close()
			args := []string{"threads", "create", "--channel", "23456789012345678", "--name", name}
			original := slices.Clone(args)
			stdout, stderr, code := runForTest(t, args, writeTestConfig(t, ""), server.URL)
			var result struct {
				OK   bool `json:"ok"`
				Data struct {
					Name string `json:"name"`
				} `json:"data"`
			}
			decodeOneJSON(t, stdout.Bytes(), &result)
			if code != 0 || stderr.Len() != 0 || !result.OK || result.Data.Name != name || !slices.Equal(args, original) {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q, argv = %q", code, stdout.String(), stderr.String(), args)
			}
		})
	}
}

func TestHelpLookingFileValuesStayOperational(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"help", "-h"} {
		if err := os.WriteFile(name, []byte("literal file content"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"post", "reply"} {
			for _, option := range []string{"--file", "--attach"} {
				t.Run(command+"/"+option+"/"+name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var payload struct {
							Content string `json:"content"`
						}
						if option == "--attach" {
							reader, err := r.MultipartReader()
							if err != nil {
								t.Error(err)
								w.WriteHeader(http.StatusBadRequest)
								return
							}
							part, err := reader.NextPart()
							if err != nil {
								t.Error(err)
								return
							}
							if err := json.NewDecoder(part).Decode(&payload); err != nil {
								t.Error(err)
							}
							part, err = reader.NextPart()
							if err != nil {
								t.Error(err)
								return
							}
							body, err := io.ReadAll(part)
							if err != nil || part.FileName() != name || string(body) != "literal file content" {
								t.Errorf("attachment = %q %q, %v", part.FileName(), body, err)
							}
						} else if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Error(err)
						}
						_, _ = io.WriteString(w, messageResponseForCLI("34567890123456789", payload.Content))
					}))
					defer server.Close()
					args := []string{"messages", command, "--channel", "23456789012345678", option, name}
					if command == "reply" {
						args = append(args, "--message", "45678901234567890")
					}
					stdout, stderr, code := runForTestWithInput(t, args, writeTestConfig(t, ""), server.URL, strings.NewReader(""))
					var result struct {
						OK   bool `json:"ok"`
						Data struct {
							ID      string `json:"id"`
							Content string `json:"content"`
						} `json:"data"`
					}
					decodeOneJSON(t, stdout.Bytes(), &result)
					wantContent := ""
					if option == "--file" {
						wantContent = "literal file content"
					}
					if code != 0 || stderr.Len() != 0 || !result.OK || result.Data.ID != "34567890123456789" || result.Data.Content != wantContent {
						t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
					}
				})
			}
		}
	}
}

func TestHelpWriteFailures(t *testing.T) {
	for _, short := range []bool{false, true} {
		var stderr bytes.Buffer
		if code := run([]string{"--help"}, helpBrokenWriter{short: short}, &stderr); code != 1 || stderr.Len() != 0 {
			t.Fatalf("short = %v, exit = %d, stderr = %q; want exit 1 without a second diagnostic", short, code, stderr.String())
		}
	}
	var stdout bytes.Buffer
	if code := run([]string{"help", "missing"}, &stdout, helpBrokenWriter{}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("invalid-help writer failure: exit = %d, stdout = %q", code, stdout.String())
	}
}

type helpPanicReader struct{}

func (helpPanicReader) Read([]byte) (int, error) { panic("help read stdin") }

type helpBrokenWriter struct{ short bool }

func (w helpBrokenWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("test write failure")
}
