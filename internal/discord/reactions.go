package discord

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

var customEmojiName = regexp.MustCompile(`^[A-Za-z0-9_]{2,32}$`)

func (c *Client) ReactionAdd(ctx context.Context, guildID, channelID, messageID, emoji string, access policy.Policy) error {
	return c.changeOwnReaction(ctx, http.MethodPut, guildID, channelID, messageID, emoji, access)
}

func (c *Client) ReactionRemove(ctx context.Context, guildID, channelID, messageID, emoji string, access policy.Policy) error {
	return c.changeOwnReaction(ctx, http.MethodDelete, guildID, channelID, messageID, emoji, access)
}

func (c *Client) changeOwnReaction(ctx context.Context, method, guildID, channelID, messageID, emoji string, access policy.Policy) error {
	if err := c.authorizeMessageTarget(ctx, access, guildID, channelID); err != nil {
		return err
	}
	if !policy.ValidSnowflake(messageID) {
		return &Error{Code: "cli.invalid_arguments", Message: "message ID must be a Discord snowflake"}
	}
	if !ValidReactionEmoji(emoji) {
		return &Error{Code: "cli.invalid_arguments", Message: "emoji must be Unicode or custom name:snowflake form"}
	}
	path := "/channels/" + channelID + "/messages/" + messageID + "/reactions/" + url.PathEscape(emoji) + "/@me"
	err := c.Do(ctx, Request{Method: method, Path: path, Idempotent: true}, nil)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusForbidden {
		return &Error{Code: "discord.reaction_access_denied", Message: "bot lacks permission to change this reaction", HTTPStatus: apiErr.HTTPStatus, DiscordCode: apiErr.DiscordCode}
	}
	return err
}

func ValidReactionEmoji(value string) bool {
	if name, id, custom := strings.Cut(value, ":"); custom {
		return !strings.Contains(id, ":") && customEmojiName.MatchString(name) && policy.ValidSnowflake(id)
	}
	if value == "" || strings.ContainsAny(value, "/\\\r\n") {
		return false
	}
	for _, character := range value {
		if character > unicode.MaxASCII && !unicode.IsControl(character) {
			return true
		}
	}
	return false
}
