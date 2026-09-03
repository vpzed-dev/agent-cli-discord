package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	threadGuild  = "12345678901234567"
	threadParent = "22345678901234567"
	threadID     = "32345678901234567"
	otherThread  = "42345678901234567"
)

func TestThreadsListFiltersByVerifiedParentAndExplicitThreadList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/"+threadGuild+"/threads/active" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"threads":[
{"id":"`+threadID+`","guild_id":"`+threadGuild+`","parent_id":"`+threadParent+`","name":"allowed","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":1440,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}},
{"id":"`+otherThread+`","guild_id":"`+threadGuild+`","parent_id":"`+threadParent+`","name":"not listed","type":99,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}},
{"id":"52345678901234567","guild_id":"`+threadGuild+`","parent_id":"99999999999999999","name":"bad parent","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}],"members":[]}`)
	}))
	defer server.Close()
	access := policy.New(threadGuild, []string{threadParent}, []string{threadID})
	threads, err := New(secretToken, Options{BaseURL: server.URL}).ThreadsList(context.Background(), threadGuild, access)
	if err != nil || len(threads) != 1 || threads[0].ID != threadID || threads[0].Type != 11 {
		t.Fatalf("threads = %#v, error = %v", threads, err)
	}
}

func TestThreadCreateSendsExplicitPublicTypeAndArchiveDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/channels/"+threadParent+"/threads" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Name        string `json:"name"`
			Type        int    `json:"type"`
			AutoArchive int    `json:"auto_archive_duration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Name != "agent work" || payload.Type != 11 || payload.AutoArchive != 1440 {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = io.WriteString(w, `{"id":"`+threadID+`","guild_id":"`+threadGuild+`","parent_id":"`+threadParent+`","name":"agent work","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":1440,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}`)
	}))
	defer server.Close()
	access := policy.New(threadGuild, []string{threadParent}, nil)
	thread, err := New(secretToken, Options{BaseURL: server.URL}).ThreadCreate(context.Background(), threadGuild, threadParent, "agent work", 1440, access)
	if err != nil || thread.ID != threadID {
		t.Fatalf("thread = %#v, error = %v", thread, err)
	}
}

func TestThreadJoinAndLeaveVerifyMetadataBeforeMutation(t *testing.T) {
	for _, operation := range []struct{ name, method string }{{"join", http.MethodPut}, {"leave", http.MethodDelete}} {
		t.Run(operation.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					if r.Method != http.MethodGet || r.URL.Path != "/channels/"+threadID {
						t.Fatalf("metadata request = %s %s", r.Method, r.URL.Path)
					}
					_, _ = io.WriteString(w, `{"id":"`+threadID+`","guild_id":"`+threadGuild+`","parent_id":"`+threadParent+`","name":"thread","type":11,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}`)
					return
				}
				if r.Method != operation.method || r.URL.Path != "/channels/"+threadID+"/thread-members/@me" {
					t.Fatalf("mutation = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			access := policy.New(threadGuild, []string{threadParent}, nil)
			client := New(secretToken, Options{BaseURL: server.URL})
			var err error
			if operation.name == "join" {
				err = client.ThreadJoin(context.Background(), threadGuild, threadID, access)
			} else {
				err = client.ThreadLeave(context.Background(), threadGuild, threadID, access)
			}
			if err != nil || requests != 2 {
				t.Fatalf("error = %v, requests = %d", err, requests)
			}
		})
	}
}

func TestArchivedThreadFailsBeforeMembershipMutation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, `{"id":"`+threadID+`","guild_id":"`+threadGuild+`","parent_id":"`+threadParent+`","name":"thread","type":11,"thread_metadata":{"archived":true,"auto_archive_duration":60,"archive_timestamp":"2026-09-03T12:00:00Z","locked":false}}`)
	}))
	defer server.Close()
	access := policy.New(threadGuild, []string{threadParent}, nil)
	err := New(secretToken, Options{BaseURL: server.URL}).ThreadJoin(context.Background(), threadGuild, threadID, access)
	if err == nil || requests != 1 {
		t.Fatalf("error = %v, requests = %d", err, requests)
	}
}

func TestThreadCreateRejectsLimitsBeforeNetwork(t *testing.T) {
	access := policy.New(threadGuild, []string{threadParent}, nil)
	client := New(secretToken, Options{})
	for _, test := range []struct {
		name     string
		duration int
	}{{"", 60}, {"x", 61}, {string(make([]byte, 101)), 60}} {
		if _, err := client.ThreadCreate(context.Background(), threadGuild, threadParent, test.name, test.duration, access); err == nil {
			t.Fatalf("test = %#v", test)
		}
	}
}

func TestThreadCreateRejectsExplicitThreadRestrictionsBeforeNetwork(t *testing.T) {
	access := policy.New(threadGuild, []string{threadParent}, []string{threadID})
	if _, err := New(secretToken, Options{}).ThreadCreate(context.Background(), threadGuild, threadParent, "new", 60, access); err == nil {
		t.Fatal("ThreadCreate() error = nil")
	}
}
