package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL          = "https://discord.com/api/v10"
	defaultUserAgent        = "DiscordBot (https://github.com/vpzed-dev/agent-cli-discord, dev)"
	defaultMaxResponseBytes = 8 << 20
	defaultMaxRetries       = 2
)

type Options struct {
	BaseURL          string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxRetries       int
	Wait             func(context.Context, time.Duration) error
}

type Request struct {
	Method      string
	Path        string
	JSONBody    []byte
	Body        io.Reader
	ContentType string
	Idempotent  bool
}

type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message,omitempty"`
	Retryable      bool   `json:"retryable"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	DiscordCode    int    `json:"discord_code,omitempty"`
	RateLimitScope string `json:"rate_limit_scope,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type Client struct {
	token            string
	baseURL          string
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxRetries       int
	wait             func(context.Context, time.Duration) error
}

func New(token string, options Options) *Client {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	maxBytes := options.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	wait := options.Wait
	if wait == nil {
		wait = waitContext
	}
	maxRetries := options.MaxRetries
	if maxRetries == 0 {
		maxRetries = defaultMaxRetries
	}
	return &Client{token: token, baseURL: baseURL, httpClient: &clone, requestTimeout: timeout, maxResponseBytes: maxBytes, maxRetries: maxRetries, wait: wait}
}

func (c *Client) Do(ctx context.Context, request Request, output any) error {
	for attempt := 0; ; attempt++ {
		response, cancel, err := c.doOnce(ctx, request)
		if err != nil {
			return err
		}
		body, readErr := readBounded(response.Body, c.maxResponseBytes)
		response.Body.Close()
		cancel()
		if readErr != nil {
			return readErr
		}

		if response.StatusCode == http.StatusTooManyRequests {
			rateErr, delay, validDelay := decodeRateLimit(response, body)
			if request.Idempotent && validDelay && attempt < c.maxRetries {
				if err := c.wait(ctx, delay); err != nil {
					return contextError(err, request)
				}
				continue
			}
			return rateErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return decodeHTTPError(response.StatusCode, body, request.Idempotent, c.token)
		}
		if output == nil || response.StatusCode == http.StatusNoContent {
			return nil
		}
		if len(body) == 0 {
			return &Error{Code: "discord.invalid_response", Message: "Discord returned an empty response"}
		}
		if err := json.Unmarshal(body, output); err != nil {
			return &Error{Code: "discord.invalid_response", Message: "Discord returned malformed JSON"}
		}
		return nil
	}
}

func (c *Client) doOnce(ctx context.Context, request Request) (*http.Response, context.CancelFunc, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	body := io.Reader(bytes.NewReader(request.JSONBody))
	if request.Body != nil {
		body = request.Body
	}
	req, err := http.NewRequestWithContext(attemptCtx, request.Method, c.baseURL+request.Path, body)
	if err != nil {
		cancel()
		return nil, nil, &Error{Code: "discord.invalid_request", Message: "could not construct Discord request"}
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", defaultUserAgent)
	if request.ContentType != "" {
		req.Header.Set("Content-Type", request.ContentType)
	} else if request.JSONBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, contextError(err, request)
	}
	return response, cancel, nil
}

func readBounded(source io.Reader, maximum int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(source, maximum+1))
	if err != nil {
		return nil, &Error{Code: "discord.invalid_response", Message: "could not read Discord response"}
	}
	if int64(len(body)) > maximum {
		return nil, &Error{Code: "discord.invalid_response", Message: "Discord response exceeded the size limit"}
	}
	return body, nil
}

func decodeHTTPError(status int, body []byte, retryable bool, token string) error {
	var value struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &value)
	message := value.Message
	if message == "" {
		message = fmt.Sprintf("Discord returned HTTP status %d", status)
	}
	if token != "" {
		message = strings.ReplaceAll(message, token, "[REDACTED]")
	}
	return &Error{Code: "discord.http_error", Message: message, Retryable: retryable && status >= 500, HTTPStatus: status, DiscordCode: value.Code}
}

func decodeRateLimit(response *http.Response, body []byte) (*Error, time.Duration, bool) {
	var value struct {
		Code       int      `json:"code"`
		Message    string   `json:"message"`
		RetryAfter *float64 `json:"retry_after"`
		Global     bool     `json:"global"`
	}
	bodyValid := json.Unmarshal(body, &value) == nil && value.RetryAfter != nil && *value.RetryAfter >= 0
	seconds := float64(0)
	if bodyValid {
		seconds = *value.RetryAfter
	}
	validDelay := bodyValid
	if header, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); err == nil && header >= 0 {
		seconds = header
		validDelay = true
	}
	scope := "route"
	if value.Global || response.Header.Get("X-RateLimit-Global") == "true" || response.Header.Get("X-RateLimit-Scope") == "global" {
		scope = "global"
	}
	return &Error{Code: "discord.rate_limited", Message: "Discord rate limit exceeded", Retryable: true, HTTPStatus: 429, DiscordCode: value.Code, RateLimitScope: scope}, time.Duration(seconds * float64(time.Second)), validDelay
}

func contextError(err error, request Request) error {
	message := "Discord request failed"
	if errors.Is(err, context.Canceled) {
		message = "Discord request canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		message = "Discord request timed out"
	}
	return &Error{Code: "discord.transport_error", Message: message, Retryable: request.Idempotent, OutcomeUnknown: !request.Idempotent}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
