package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

type ReadOptions struct {
	Limit  int
	Before string
	After  string
	Around string
}

type Cursor struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type MessagePage struct {
	Messages []Message `json:"messages"`
	Cursor   *Cursor   `json:"cursor,omitempty"`
}

type Message struct {
	ID                      string            `json:"id"`
	ChannelID               string            `json:"channel_id"`
	Author                  MessageAuthor     `json:"author"`
	Content                 string            `json:"content"`
	Timestamp               time.Time         `json:"timestamp"`
	EditedTimestamp         *time.Time        `json:"edited_timestamp"`
	Attachments             []Attachment      `json:"attachments"`
	Embeds                  []json.RawMessage `json:"embeds"`
	MessageReference        *MessageReference `json:"message_reference,omitempty"`
	ReferencedMessage       *Message          `json:"referenced_message,omitempty"`
	Type                    int               `json:"type"`
	ContentMayBeUnavailable bool              `json:"content_may_be_unavailable,omitempty"`
}

type MessageAuthor struct {
	ID         string  `json:"id"`
	Username   string  `json:"username"`
	GlobalName *string `json:"global_name"`
	Bot        bool    `json:"bot"`
}

type Attachment struct {
	ID          string  `json:"id"`
	Filename    string  `json:"filename"`
	Description *string `json:"description,omitempty"`
	ContentType *string `json:"content_type,omitempty"`
	Size        int64   `json:"size"`
	URL         string  `json:"url"`
	ProxyURL    string  `json:"proxy_url,omitempty"`
}

type MessageReference struct {
	MessageID string `json:"message_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	GuildID   string `json:"guild_id,omitempty"`
}

func (c *Client) MessagesRead(ctx context.Context, guildID, channelID string, access policy.Policy, options ReadOptions) (MessagePage, error) {
	if err := c.authorizeMessageTarget(ctx, access, guildID, channelID); err != nil {
		return MessagePage{}, err
	}
	limit, err := validateReadOptions(options)
	if err != nil {
		return MessagePage{}, err
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if options.Before != "" {
		query.Set("before", options.Before)
	}
	if options.After != "" {
		query.Set("after", options.After)
	}
	if options.Around != "" {
		query.Set("around", options.Around)
	}

	var messages []Message
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/channels/" + channelID + "/messages?" + query.Encode(), Idempotent: true}, &messages); err != nil {
		return MessagePage{}, err
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	markContentSymptoms(messages)
	page := MessagePage{Messages: messages}
	if len(messages) > 0 && options.Around == "" {
		page.Cursor = &Cursor{Before: messages[0].ID}
		if options.After != "" {
			page.Cursor = &Cursor{After: messages[len(messages)-1].ID}
		}
	}
	return page, nil
}

func (c *Client) MessageGet(ctx context.Context, guildID, channelID, messageID string, access policy.Policy) (Message, error) {
	if err := c.authorizeMessageTarget(ctx, access, guildID, channelID); err != nil {
		return Message{}, err
	}
	if !policy.ValidSnowflake(messageID) {
		return Message{}, &Error{Code: "cli.invalid_arguments", Message: "message ID must be a Discord snowflake"}
	}
	var message Message
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/channels/" + channelID + "/messages/" + messageID, Idempotent: true}, &message); err != nil {
		return Message{}, err
	}
	messages := []Message{message}
	markContentSymptoms(messages)
	return messages[0], nil
}

func (c *Client) authorizeMessageTarget(ctx context.Context, access policy.Policy, guildID, channelID string) error {
	if access.AuthorizeChannel(guildID, channelID) == nil {
		return nil
	}
	if !policy.ValidSnowflake(channelID) || access.AuthorizeGuild(guildID) != nil {
		return messageTargetNotAuthorized()
	}

	var thread Thread
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/channels/" + channelID, Idempotent: true}, &thread); err != nil {
		return err
	}
	if thread.ID != channelID || thread.GuildID != guildID || thread.ThreadMetadata == nil ||
		(thread.Type != 10 && thread.Type != 11 && thread.Type != 12) ||
		access.AuthorizeThread(guildID, channelID, thread.ParentID) != nil {
		return messageTargetNotAuthorized()
	}
	return nil
}

func messageTargetNotAuthorized() error {
	return &Error{Code: "policy.channel_not_authorized", Message: "channel or thread is not authorized"}
}

func validateReadOptions(options ReadOptions) (int, error) {
	limit := options.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return 0, &Error{Code: "cli.invalid_arguments", Message: "limit must be between 1 and 100"}
	}
	ids := []string{options.Before, options.After, options.Around}
	count := 0
	for _, id := range ids {
		if id != "" {
			count++
			if !policy.ValidSnowflake(id) {
				return 0, &Error{Code: "cli.invalid_arguments", Message: "cursor must be a Discord snowflake"}
			}
		}
	}
	if count > 1 {
		return 0, &Error{Code: "cli.invalid_arguments", Message: "before, after, and around are mutually exclusive"}
	}
	return limit, nil
}

func markContentSymptoms(messages []Message) {
	for index := range messages {
		message := &messages[index]
		message.ContentMayBeUnavailable = message.Type == 0 && !message.Author.Bot && message.Content == "" && len(message.Attachments) == 0 && len(message.Embeds) == 0
	}
}
