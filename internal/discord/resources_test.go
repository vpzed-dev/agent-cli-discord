package discord

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	resourceGuild      = "12345678901234567"
	resourceAllowed    = "22345678901234567"
	resourceUnknown    = "32345678901234567"
	resourceDisallowed = "42345678901234567"
)

func TestAuthCheckUsesCurrentUserAndProjectsBotIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/users/@me" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"agent","global_name":"Agent Bot","discriminator":"0","avatar":"hash","bot":true,"future_field":"ignored"}`)
	}))
	defer server.Close()

	identity, err := New(secretToken, Options{BaseURL: server.URL}).AuthCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != resourceGuild || identity.Username != "agent" || identity.GlobalName == nil || *identity.GlobalName != "Agent Bot" || !identity.Bot {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestAuthCheckRejectsNonBotIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"12345678901234567","username":"person","bot":false}`)
	}))
	defer server.Close()

	_, err := New(secretToken, Options{BaseURL: server.URL}).AuthCheck(context.Background())
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.not_bot_identity" {
		t.Fatalf("error = %#v", err)
	}
}

func TestChannelsListUsesGuildEndpointAndFiltersLocalAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/guilds/"+resourceGuild+"/channels" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `[
 {"id":"`+resourceAllowed+`","type":0,"guild_id":"`+resourceGuild+`","position":2,"name":"agents","parent_id":null},
 {"id":"`+resourceDisallowed+`","type":0,"guild_id":"`+resourceGuild+`","position":1,"name":"private"},
 {"id":"`+resourceUnknown+`","type":99,"guild_id":"`+resourceGuild+`","position":3,"name":"future","future_field":true}
]`)
	}))
	defer server.Close()

	access := policy.New(resourceGuild, []string{resourceAllowed, resourceUnknown}, nil)
	channels, err := New(secretToken, Options{BaseURL: server.URL}).ChannelsList(context.Background(), resourceGuild, access)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 || channels[0].ID != resourceAllowed || channels[1].ID != resourceUnknown || channels[1].Type != 99 {
		t.Fatalf("channels = %#v", channels)
	}
	if channels[0].Name == nil || *channels[0].Name != "agents" || channels[0].ParentID != nil {
		t.Fatalf("projected channel = %#v", channels[0])
	}
}

func TestChannelsListRejectsWrongGuildBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	access := policy.New(resourceGuild, []string{resourceAllowed}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).ChannelsList(context.Background(), resourceDisallowed, access)
	if err == nil || requests != 0 {
		t.Fatalf("error = %v, requests = %d", err, requests)
	}
}

func TestChannelsListMapsForbiddenToActionableError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":50013,"message":"Missing Permissions"}`)
	}))
	defer server.Close()

	access := policy.New(resourceGuild, []string{resourceAllowed}, nil)
	_, err := New(secretToken, Options{BaseURL: server.URL}).ChannelsList(context.Background(), resourceGuild, access)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.guild_access_denied" || !strings.Contains(apiErr.Message, "guild member") {
		t.Fatalf("error = %#v", err)
	}
}
