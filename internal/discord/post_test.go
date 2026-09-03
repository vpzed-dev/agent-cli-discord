package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	postGuild   = "12345678901234567"
	postChannel = "22345678901234567"
	postReply   = "32345678901234567"
)

func TestMessagePostUsesSafeMentionDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/channels/"+postChannel+"/messages" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse       []string `json:"parse"`
				RepliedUser bool     `json:"replied_user"`
			} `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Content != "@everyone hello" || payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 || payload.AllowedMentions.RepliedUser {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = io.WriteString(w, messageResponse("42345678901234567", payload.Content))
	}))
	defer server.Close()

	access := policy.New(postGuild, []string{postChannel}, nil)
	message, err := New(secretToken, Options{BaseURL: server.URL}).MessagePost(context.Background(), postGuild, postChannel, access, PostOptions{Content: "@everyone hello"})
	if err != nil || message.ID != "42345678901234567" {
		t.Fatalf("message = %#v, error = %v", message, err)
	}
}

func TestMessageReplyRequiresExistingReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Reference struct {
				MessageID       string `json:"message_id"`
				ChannelID       string `json:"channel_id"`
				GuildID         string `json:"guild_id"`
				FailIfNotExists bool   `json:"fail_if_not_exists"`
			} `json:"message_reference"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Reference.MessageID != postReply || payload.Reference.ChannelID != postChannel || payload.Reference.GuildID != postGuild || !payload.Reference.FailIfNotExists {
			t.Fatalf("reference = %#v", payload.Reference)
		}
		_, _ = io.WriteString(w, messageResponse("42345678901234567", "reply"))
	}))
	defer server.Close()

	access := policy.New(postGuild, []string{postChannel}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).MessagePost(context.Background(), postGuild, postChannel, access, PostOptions{Content: "reply", ReplyTo: postReply})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMessagePostToThreadVerifiesParentBeforeMutation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if r.Method != http.MethodGet || r.URL.Path != "/channels/"+messageThread {
				t.Fatalf("metadata request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"id":"`+messageThread+`","guild_id":"`+postGuild+`","parent_id":"`+postChannel+`","name":"work","type":11,"thread_metadata":{"archived":false}}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/channels/"+messageThread+"/messages" {
			t.Fatalf("message request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, messageResponse("42345678901234567", "thread message"))
	}))
	defer server.Close()

	access := policy.New(postGuild, []string{postChannel}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).MessagePost(context.Background(), postGuild, messageThread, access, PostOptions{Content: "thread message"})
	if err != nil || requests != 2 {
		t.Fatalf("error = %v, requests = %d", err, requests)
	}
}

func TestMessagePostRejectsInvalidInputBeforeNetwork(t *testing.T) {
	access := policy.New(postGuild, []string{postChannel}, nil)
	client := New(secretToken, Options{})
	for name, test := range map[string]struct {
		channel string
		options PostOptions
	}{
		"empty":        {postChannel, PostOptions{}},
		"too long":     {postChannel, PostOptions{Content: strings.Repeat("x", 2001)}},
		"invalid UTF8": {postChannel, PostOptions{Content: string([]byte{0xff})}},
		"bad reply":    {postChannel, PostOptions{Content: "x", ReplyTo: "not-an-id"}},
		"bad channel":  {"not-a-snowflake", PostOptions{Content: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.MessagePost(context.Background(), postGuild, test.channel, access, test.options); err == nil {
				t.Fatal("MessagePost() error = nil")
			}
		})
	}
}

func TestMessagePostStreamsNumberedMultipartAttachments(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "one.txt")
	second := filepath.Join(directory, "two.bin")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string]string{}
		filenames := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(part)
			parts[part.FormName()] = string(raw)
			filenames[part.FormName()] = part.FileName()
		}
		var payload struct {
			Content     string `json:"content"`
			Attachments []struct {
				ID       int    `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal([]byte(parts["payload_json"]), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Content != "files" || len(payload.Attachments) != 2 || payload.Attachments[0].ID != 0 || payload.Attachments[1].ID != 1 || filenames["files[0]"] != "one.txt" || filenames["files[1]"] != "two.bin" || parts["files[0]"] != "one" {
			t.Fatalf("payload = %#v, filenames = %#v, parts = %#v", payload, filenames, parts)
		}
		_, _ = io.WriteString(w, messageResponse("42345678901234567", "files"))
	}))
	defer server.Close()
	access := policy.New(postGuild, []string{postChannel}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).MessagePost(context.Background(), postGuild, postChannel, access, PostOptions{Content: "files", AttachmentPaths: []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMessagePostRejectsAttachmentFailureBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	oversized := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	access := policy.New(postGuild, []string{postChannel}, nil)
	client := New(secretToken, Options{BaseURL: server.URL})
	for _, path := range []string{filepath.Join(t.TempDir(), "missing"), oversized, t.TempDir()} {
		if _, err := client.MessagePost(context.Background(), postGuild, postChannel, access, PostOptions{AttachmentPaths: []string{path}}); err == nil {
			t.Fatalf("attachment %q error = nil", path)
		}
	}
	if requests != 0 {
		t.Fatalf("requests = %d", requests)
	}
}

func messageResponse(id, content string) string {
	raw, _ := json.Marshal(map[string]any{
		"id": id, "channel_id": postChannel, "author": map[string]any{"id": "52345678901234567", "username": "agent", "bot": true},
		"content": content, "timestamp": "2026-09-03T12:00:00Z", "attachments": []any{}, "embeds": []any{}, "type": 0,
	})
	return string(raw)
}
