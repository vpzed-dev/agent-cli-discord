package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

type Thread struct {
	ID             string          `json:"id"`
	GuildID        string          `json:"guild_id"`
	ParentID       string          `json:"parent_id"`
	Name           string          `json:"name"`
	Type           int             `json:"type"`
	ThreadMetadata *ThreadMetadata `json:"thread_metadata"`
}

type ThreadMetadata struct {
	Archived            bool   `json:"archived"`
	AutoArchiveDuration int    `json:"auto_archive_duration"`
	ArchiveTimestamp    string `json:"archive_timestamp"`
	Locked              bool   `json:"locked"`
}

func (c *Client) ThreadsList(ctx context.Context, guildID string, access policy.Policy) ([]Thread, error) {
	if err := access.AuthorizeGuild(guildID); err != nil {
		return nil, &Error{Code: "policy.guild_not_authorized", Message: "guild is not authorized"}
	}
	var response struct {
		Threads []Thread `json:"threads"`
	}
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/guilds/" + guildID + "/threads/active", Idempotent: true}, &response); err != nil {
		return nil, err
	}
	allowed := make([]Thread, 0, len(response.Threads))
	for _, thread := range response.Threads {
		if thread.GuildID == guildID && thread.ThreadMetadata != nil && access.AuthorizeThread(guildID, thread.ID, thread.ParentID) == nil {
			allowed = append(allowed, thread)
		}
	}
	return allowed, nil
}

func (c *Client) ThreadCreate(ctx context.Context, guildID, parentID, name string, autoArchiveDuration int, access policy.Policy) (Thread, error) {
	if err := access.AuthorizeChannel(guildID, parentID); err != nil {
		return Thread{}, &Error{Code: "policy.channel_not_authorized", Message: "parent channel is not authorized"}
	}
	if access.HasExplicitThreadRestrictions() {
		return Thread{}, &Error{Code: "policy.thread_creation_restricted", Message: "cannot create a new thread while explicit thread restrictions are configured"}
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return Thread{}, &Error{Code: "cli.invalid_arguments", Message: "thread name must contain 1 to 100 UTF-8 characters"}
	}
	if !validAutoArchiveDuration(autoArchiveDuration) {
		return Thread{}, &Error{Code: "cli.invalid_arguments", Message: "auto archive duration must be 60, 1440, 4320, or 10080 minutes"}
	}
	payload, _ := json.Marshal(struct {
		Name                string `json:"name"`
		AutoArchiveDuration int    `json:"auto_archive_duration"`
		Type                int    `json:"type"`
	}{Name: name, AutoArchiveDuration: autoArchiveDuration, Type: 11})
	var thread Thread
	if err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/channels/" + parentID + "/threads", JSONBody: payload}, &thread); err != nil {
		return Thread{}, err
	}
	if thread.GuildID != guildID || thread.ParentID != parentID {
		return Thread{}, &Error{Code: "discord.invalid_response", Message: "Discord returned a thread outside the authorized parent"}
	}
	return thread, nil
}

func (c *Client) ThreadJoin(ctx context.Context, guildID, threadID string, access policy.Policy) error {
	return c.changeThreadMembership(ctx, http.MethodPut, guildID, threadID, access)
}

func (c *Client) ThreadLeave(ctx context.Context, guildID, threadID string, access policy.Policy) error {
	return c.changeThreadMembership(ctx, http.MethodDelete, guildID, threadID, access)
}

func (c *Client) changeThreadMembership(ctx context.Context, method, guildID, threadID string, access policy.Policy) error {
	if err := access.AuthorizeGuild(guildID); err != nil || !policy.ValidSnowflake(threadID) {
		return &Error{Code: "policy.thread_not_authorized", Message: "thread is not authorized"}
	}
	var thread Thread
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/channels/" + threadID, Idempotent: true}, &thread); err != nil {
		return err
	}
	if thread.ID != threadID || thread.GuildID != guildID || thread.ThreadMetadata == nil || (thread.Type != 10 && thread.Type != 11 && thread.Type != 12) || access.AuthorizeThread(guildID, threadID, thread.ParentID) != nil {
		return &Error{Code: "policy.thread_not_authorized", Message: "thread metadata does not satisfy local authorization policy"}
	}
	if thread.ThreadMetadata.Archived {
		return &Error{Code: "discord.thread_archived", Message: "archived threads cannot be joined or left"}
	}
	return c.Do(ctx, Request{Method: method, Path: "/channels/" + threadID + "/thread-members/@me", Idempotent: true}, nil)
}

func validAutoArchiveDuration(value int) bool {
	return value == 60 || value == 1440 || value == 4320 || value == 10080
}
