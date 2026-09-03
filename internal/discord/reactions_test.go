package discord

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	reactionGuild   = "12345678901234567"
	reactionChannel = "22345678901234567"
	reactionMessage = "32345678901234567"
)

func TestReactionAddAndRemoveEncodeUnicodeAndCustomEmoji(t *testing.T) {
	tests := []struct {
		name, method, emoji, escaped string
	}{
		{"add Unicode", http.MethodPut, "🔥", "%F0%9F%94%A5"},
		{"remove custom", http.MethodDelete, "party:42345678901234567", "party:42345678901234567"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/channels/" + reactionChannel + "/messages/" + reactionMessage + "/reactions/" + test.escaped + "/@me"
				if r.Method != test.method || r.URL.EscapedPath() != want {
					t.Fatalf("request = %s %s, want %s %s", r.Method, r.URL.EscapedPath(), test.method, want)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			access := policy.New(reactionGuild, []string{reactionChannel}, nil)
			client := New(secretToken, Options{BaseURL: server.URL})
			var err error
			if test.method == http.MethodPut {
				err = client.ReactionAdd(context.Background(), reactionGuild, reactionChannel, reactionMessage, test.emoji, access)
			} else {
				err = client.ReactionRemove(context.Background(), reactionGuild, reactionChannel, reactionMessage, test.emoji, access)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReactionRejectsInvalidInputBeforeNetwork(t *testing.T) {
	access := policy.New(reactionGuild, []string{reactionChannel}, nil)
	client := New(secretToken, Options{})
	for name, values := range map[string][3]string{
		"channel":     {"not-a-snowflake", reactionMessage, "🔥"},
		"message":     {reactionChannel, "bad", "🔥"},
		"empty emoji": {reactionChannel, reactionMessage, ""},
		"emoji name":  {reactionChannel, reactionMessage, "party"},
		"bad custom":  {reactionChannel, reactionMessage, "party:bad"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := client.ReactionAdd(context.Background(), reactionGuild, values[0], values[1], values[2], access); err == nil {
				t.Fatal("ReactionAdd() error = nil")
			}
		})
	}
}

func TestReactionMapsPermissionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	access := policy.New(reactionGuild, []string{reactionChannel}, nil)
	err := New(secretToken, Options{BaseURL: server.URL}).ReactionAdd(context.Background(), reactionGuild, reactionChannel, reactionMessage, "🔥", access)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.reaction_access_denied" {
		t.Fatalf("error = %#v", err)
	}
}

func TestReactionInThreadVerifiesParentBeforeMutation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if r.Method != http.MethodGet || r.URL.Path != "/channels/"+messageThread {
				t.Fatalf("metadata request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"id":"` + messageThread + `","guild_id":"` + reactionGuild + `","parent_id":"` + reactionChannel + `","name":"work","type":11,"thread_metadata":{"archived":false}}`))
			return
		}
		want := "/channels/" + messageThread + "/messages/" + reactionMessage + "/reactions/%F0%9F%94%A5/@me"
		if r.Method != http.MethodPut || r.URL.EscapedPath() != want {
			t.Fatalf("reaction request = %s %s", r.Method, r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	access := policy.New(reactionGuild, []string{reactionChannel}, nil)
	err := New(secretToken, Options{BaseURL: server.URL}).ReactionAdd(context.Background(), reactionGuild, messageThread, reactionMessage, "🔥", access)
	if err != nil || requests != 2 {
		t.Fatalf("error = %v, requests = %d", err, requests)
	}
}
