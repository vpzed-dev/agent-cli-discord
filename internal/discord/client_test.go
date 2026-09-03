package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const secretToken = "secret-token-never-disclose"

func TestDoBuildsV10BotRequestAndDecodesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v10/channels/123/messages" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bot "+secretToken {
			t.Fatal("missing bot authorization")
		}
		if r.Header.Get("User-Agent") != "DiscordBot (https://github.com/vpzed-dev/agent-cli-discord, dev)" {
			t.Fatalf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"content":"hello"}` {
			t.Fatalf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"456"}`)
	}))
	defer server.Close()

	client := New(secretToken, Options{BaseURL: server.URL + "/api/v10", HTTPClient: server.Client()})
	var result struct {
		ID string `json:"id"`
	}
	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/channels/123/messages", JSONBody: []byte(`{"content":"hello"}`)}, &result)
	if err != nil || result.ID != "456" {
		t.Fatalf("Do() result = %#v, error = %v", result, err)
	}
}

func TestDoMapsDiscordErrorWithoutDisclosingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":50013,"message":"Missing Permissions: `+secretToken+`"}`)
	}))
	defer server.Close()

	err := New(secretToken, Options{BaseURL: server.URL}).Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 403 || apiErr.DiscordCode != 50013 || apiErr.Code != "discord.http_error" {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatal("error disclosed token")
	}
}

func TestDoRejectsInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "malformed": "not-json", "oversized": strings.Repeat("x", 65)} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			var out map[string]any
			err := New(secretToken, Options{BaseURL: server.URL, MaxResponseBytes: 64}).Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, &out)
			if err == nil {
				t.Fatal("Do() error = nil")
			}
		})
	}
}

func TestDoRetriesBoundedIdempotentRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.Header().Set("X-RateLimit-Scope", "global")
			w.Header().Set("Retry-After", "0.25")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"limited","retry_after":0.25,"global":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	var waited time.Duration
	client := New(secretToken, Options{BaseURL: server.URL, MaxRetries: 1, Wait: func(_ context.Context, duration time.Duration) error { waited = duration; return nil }})
	var out map[string]any
	if err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, &out); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || waited != 250*time.Millisecond {
		t.Fatalf("attempts = %d, waited = %s", attempts, waited)
	}
}

func TestDoUsesBoundedDefaultForIdempotentRateLimits(t *testing.T) {
	attempts := 0
	waits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"limited","retry_after":0,"global":false}`)
	}))
	defer server.Close()

	client := New(secretToken, Options{
		BaseURL: server.URL,
		Wait: func(context.Context, time.Duration) error {
			waits++
			return nil
		},
	})
	err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.rate_limited" {
		t.Fatalf("error = %#v", err)
	}
	if attempts != 3 || waits != 2 {
		t.Fatalf("attempts = %d, waits = %d; want default bound of 3 attempts and 2 waits", attempts, waits)
	}
}

func TestDoDoesNotRetryNonIdempotentRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"limited","retry_after":1,"global":false}`)
	}))
	defer server.Close()

	err := New(secretToken, Options{BaseURL: server.URL, MaxRetries: 3}).Do(context.Background(), Request{Method: http.MethodPost, Path: "/test"}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.RateLimitScope != "route" || attempts != 1 {
		t.Fatalf("error = %#v, attempts = %d", err, attempts)
	}
}

func TestDoStopsAtConfiguredRetryBound(t *testing.T) {
	attempts := 0
	waits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"limited","retry_after":0.01,"global":false}`)
	}))
	defer server.Close()

	client := New(secretToken, Options{
		BaseURL:    server.URL,
		MaxRetries: 2,
		Wait: func(context.Context, time.Duration) error {
			waits++
			return nil
		},
	})
	err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.rate_limited" {
		t.Fatalf("error = %#v", err)
	}
	if attempts != 3 || waits != 2 {
		t.Fatalf("attempts = %d, waits = %d; want 3 and 2", attempts, waits)
	}
}

func TestDoCancelsWhileWaitingForRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"limited","retry_after":60,"global":false}`)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := New(secretToken, Options{
		BaseURL:    server.URL,
		MaxRetries: 1,
		Wait: func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		},
	})
	err := client.Do(ctx, Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Message != "Discord request canceled" {
		t.Fatalf("error = %#v", err)
	}
}

func TestDoEnforcesPerAttemptRequestTimeout(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client := New(secretToken, Options{
		HTTPClient:     &http.Client{Transport: transport},
		RequestTimeout: time.Millisecond,
	})
	err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Message != "Discord request timed out" || !apiErr.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func TestDoKeepsAttemptContextAliveWhileReadingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, `{"id":"456"}`)
	}))
	defer server.Close()

	var out struct {
		ID string `json:"id"`
	}
	err := New(secretToken, Options{BaseURL: server.URL, RequestTimeout: time.Second}).Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, &out)
	if err != nil || out.ID != "456" {
		t.Fatalf("result = %#v, error = %v", out, err)
	}
}

func TestDoUsesRetryAfterHeaderWhenRateLimitJSONIsMalformed(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0.125")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `not-json`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	var waited time.Duration
	client := New(secretToken, Options{BaseURL: server.URL, MaxRetries: 1, Wait: func(_ context.Context, delay time.Duration) error { waited = delay; return nil }})
	if err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil); err != nil {
		t.Fatal(err)
	}
	if waited != 125*time.Millisecond {
		t.Fatalf("waited = %s", waited)
	}
}

func TestDoDoesNotRetryRateLimitWithoutValidDelay(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"limited","retry_after":"invalid"}`)
	}))
	defer server.Close()

	err := New(secretToken, Options{BaseURL: server.URL, MaxRetries: 1}).Do(context.Background(), Request{Method: http.MethodGet, Path: "/test", Idempotent: true}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "discord.rate_limited" || attempts != 1 {
		t.Fatalf("error = %#v, attempts = %d", err, attempts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestDoMarksAmbiguousMutationAndHonorsCancellation(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(secretToken, Options{HTTPClient: &http.Client{Transport: transport}}).Do(ctx, Request{Method: http.MethodPost, Path: "/test"}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || !apiErr.OutcomeUnknown || apiErr.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func TestErrorIsValidJSONMetadata(t *testing.T) {
	raw, err := json.Marshal(&Error{Code: "x", HTTPStatus: 429, DiscordCode: 1, RateLimitScope: "global"})
	if err != nil || !json.Valid(raw) {
		t.Fatalf("marshal error metadata: %v", err)
	}
}
