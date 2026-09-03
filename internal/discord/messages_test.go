package discord

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	messageGuild   = "12345678901234567"
	messageChannel = "22345678901234567"
	messageThread  = "72345678901234567"
	messageOld     = "32345678901234567"
	messageNew     = "42345678901234567"
)

func TestMessagesReadValidatesPaginationBeforeNetwork(t *testing.T) {
	access := policy.New(messageGuild, []string{messageChannel}, nil)
	client := New(secretToken, Options{})
	tests := []ReadOptions{
		{Limit: -1}, {Limit: 101},
		{Limit: 1, Before: messageOld, After: messageNew},
		{Limit: 1, Before: "name"},
	}
	for _, options := range tests {
		if _, err := client.MessagesRead(context.Background(), messageGuild, messageChannel, access, options); err == nil {
			t.Fatalf("MessagesRead(%#v) error = nil", options)
		}
	}
}

func TestMessagesReadRequestsDiscordOrderAndReturnsChronologicalResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channels/"+messageChannel+"/messages" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != messageOld {
			t.Fatalf("URL = %s", r.URL.String())
		}
		_, _ = io.WriteString(w, `[
{"id":"`+messageNew+`","channel_id":"`+messageChannel+`","author":{"id":"52345678901234567","username":"bot","bot":true},"content":"new","timestamp":"2026-09-03T12:00:01Z","edited_timestamp":null,"attachments":[],"embeds":[],"type":0},
{"id":"`+messageOld+`","channel_id":"`+messageChannel+`","author":{"id":"62345678901234567","username":"human","bot":false},"content":"","timestamp":"2026-09-03T12:00:00Z","edited_timestamp":null,"attachments":[],"embeds":[],"message_reference":{"message_id":"72345678901234567","channel_id":"`+messageChannel+`","guild_id":"`+messageGuild+`"},"type":0}
]`)
	}))
	defer server.Close()

	access := policy.New(messageGuild, []string{messageChannel}, nil)
	result, err := New(secretToken, Options{BaseURL: server.URL}).MessagesRead(context.Background(), messageGuild, messageChannel, access, ReadOptions{Limit: 2, Before: messageOld})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 || result.Messages[0].ID != messageOld || result.Messages[1].ID != messageNew {
		t.Fatalf("messages = %#v", result.Messages)
	}
	if result.Cursor == nil || result.Cursor.Before != messageOld || result.Cursor.After != "" {
		t.Fatalf("cursor = %#v", result.Cursor)
	}
	if !result.Messages[0].ContentMayBeUnavailable || result.Messages[0].MessageReference == nil {
		t.Fatalf("empty message diagnostics/reference = %#v", result.Messages[0])
	}
}

func TestMessagesReadAfterProducesForwardCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":"`+messageNew+`","channel_id":"`+messageChannel+`","author":{"id":"52345678901234567","username":"bot","bot":true},"content":"x","timestamp":"2026-09-03T12:00:01Z","attachments":[],"embeds":[],"type":0}]`)
	}))
	defer server.Close()
	access := policy.New(messageGuild, []string{messageChannel}, nil)
	result, err := New(secretToken, Options{BaseURL: server.URL}).MessagesRead(context.Background(), messageGuild, messageChannel, access, ReadOptions{Limit: 1, After: messageOld})
	if err != nil || result.Cursor == nil || result.Cursor.After != messageNew {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestMessageGetProjectsAttachmentsEmbedsAndReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channels/"+messageChannel+"/messages/"+messageNew {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"`+messageNew+`","channel_id":"`+messageChannel+`","author":{"id":"52345678901234567","username":"agent","global_name":null,"bot":true},"content":"hello","timestamp":"2026-09-03T12:00:01Z","attachments":[{"id":"82345678901234567","filename":"note.txt","size":5,"url":"https://cdn.discordapp.com/a"}],"embeds":[{"title":"title","future":true}],"message_reference":{"message_id":"72345678901234567","channel_id":"`+messageChannel+`"},"type":19,"future_field":true}`)
	}))
	defer server.Close()
	access := policy.New(messageGuild, []string{messageChannel}, nil)
	message, err := New(secretToken, Options{BaseURL: server.URL}).MessageGet(context.Background(), messageGuild, messageChannel, messageNew, access)
	if err != nil || len(message.Attachments) != 1 || len(message.Embeds) != 1 || message.MessageReference == nil {
		t.Fatalf("message = %#v, error = %v", message, err)
	}
}

func TestMessagesReadAuthorizesThreadThroughVerifiedParent(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/channels/"+messageThread {
				t.Fatalf("metadata request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"id":"`+messageThread+`","guild_id":"`+messageGuild+`","parent_id":"`+messageChannel+`","name":"work","type":11,"thread_metadata":{"archived":false}}`)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/channels/"+messageThread+"/messages" {
				t.Fatalf("message request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `[]`)
		default:
			t.Fatalf("unexpected request %d: %s %s", requests, r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	access := policy.New(messageGuild, []string{messageChannel}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).MessagesRead(context.Background(), messageGuild, messageThread, access, ReadOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want metadata then message request", requests)
	}
}

func TestMessageTargetRejectsInvalidThreadMetadataBeforeContentRequest(t *testing.T) {
	tests := map[string]string{
		"disallowed parent": `{"id":"` + messageThread + `","guild_id":"` + messageGuild + `","parent_id":"99999999999999999","type":11,"thread_metadata":{"archived":false}}`,
		"wrong guild":       `{"id":"` + messageThread + `","guild_id":"99999999999999999","parent_id":"` + messageChannel + `","type":11,"thread_metadata":{"archived":false}}`,
		"wrong ID":          `{"id":"82345678901234567","guild_id":"` + messageGuild + `","parent_id":"` + messageChannel + `","type":11,"thread_metadata":{"archived":false}}`,
		"not a thread":      `{"id":"` + messageThread + `","guild_id":"` + messageGuild + `","parent_id":"` + messageChannel + `","type":0}`,
	}
	for name, metadata := range tests {
		t.Run(name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/channels/"+messageThread {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, metadata)
			}))
			defer server.Close()

			access := policy.New(messageGuild, []string{messageChannel}, nil)
			_, err := New(secretToken, Options{BaseURL: server.URL}).MessageGet(context.Background(), messageGuild, messageThread, messageNew, access)
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Code != "policy.channel_not_authorized" {
				t.Fatalf("error = %#v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want metadata request only", requests)
			}
		})
	}
}

func TestMessageTargetEnforcesExplicitThreadAllowlist(t *testing.T) {
	for _, test := range []struct {
		name      string
		threadIDs []string
		wantOK    bool
	}{
		{name: "listed", threadIDs: []string{messageThread}, wantOK: true},
		{name: "unlisted", threadIDs: []string{"82345678901234567"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					_, _ = io.WriteString(w, `{"id":"`+messageThread+`","guild_id":"`+messageGuild+`","parent_id":"`+messageChannel+`","name":"work","type":11,"thread_metadata":{"archived":false}}`)
					return
				}
				_, _ = io.WriteString(w, `[]`)
			}))
			defer server.Close()

			access := policy.New(messageGuild, []string{messageChannel}, test.threadIDs)
			_, err := New(secretToken, Options{BaseURL: server.URL}).MessagesRead(context.Background(), messageGuild, messageThread, access, ReadOptions{Limit: 1})
			if test.wantOK && err != nil {
				t.Fatal(err)
			}
			if !test.wantOK {
				var apiErr *Error
				if !errors.As(err, &apiErr) || apiErr.Code != "policy.channel_not_authorized" || requests != 1 {
					t.Fatalf("error = %#v, requests = %d", err, requests)
				}
			}
		})
	}
}

func TestMessageOperationsRejectMalformedTargetBeforeNetwork(t *testing.T) {
	access := policy.New(messageGuild, []string{messageChannel}, nil)
	client := New(secretToken, Options{})
	_, readErr := client.MessagesRead(context.Background(), messageGuild, "not-a-snowflake", access, ReadOptions{Limit: 1})
	_, getErr := client.MessageGet(context.Background(), messageGuild, "not-a-snowflake", messageNew, access)
	var readAPIError, getAPIError *Error
	if !errors.As(readErr, &readAPIError) || !errors.As(getErr, &getAPIError) || readAPIError.Code != "policy.channel_not_authorized" || getAPIError.Code != "policy.channel_not_authorized" {
		t.Fatalf("read error = %#v, get error = %#v", readErr, getErr)
	}
}
