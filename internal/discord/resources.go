package discord

import (
	"context"
	"errors"
	"net/http"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

type Identity struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	GlobalName    *string `json:"global_name"`
	Discriminator string  `json:"discriminator"`
	Avatar        *string `json:"avatar"`
	Bot           bool    `json:"bot"`
}

type Channel struct {
	ID       string  `json:"id"`
	Type     int     `json:"type"`
	GuildID  string  `json:"guild_id"`
	Position int     `json:"position"`
	Name     *string `json:"name"`
	ParentID *string `json:"parent_id"`
}

func (c *Client) AuthCheck(ctx context.Context) (Identity, error) {
	var identity Identity
	if err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/users/@me", Idempotent: true}, &identity); err != nil {
		return Identity{}, err
	}
	if !identity.Bot {
		return Identity{}, &Error{Code: "discord.not_bot_identity", Message: "Discord credential did not identify a bot user"}
	}
	return identity, nil
}

func (c *Client) ChannelsList(ctx context.Context, guildID string, access policy.Policy) ([]Channel, error) {
	if err := access.AuthorizeGuild(guildID); err != nil {
		return nil, &Error{Code: "policy.guild_not_authorized", Message: "guild is not authorized"}
	}

	var received []Channel
	err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/guilds/" + guildID + "/channels", Idempotent: true}, &received)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusForbidden {
			return nil, &Error{
				Code:        "discord.guild_access_denied",
				Message:     "bot cannot access the configured guild; confirm it is a guild member and can view the intended channels",
				HTTPStatus:  apiErr.HTTPStatus,
				DiscordCode: apiErr.DiscordCode,
			}
		}
		return nil, err
	}

	allowed := make([]Channel, 0, len(received))
	for _, channel := range received {
		if access.AuthorizeChannel(guildID, channel.ID) == nil {
			allowed = append(allowed, channel)
		}
	}
	return allowed, nil
}
