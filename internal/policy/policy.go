package policy

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var snowflakePattern = regexp.MustCompile(`^[0-9]{17,20}$`)

func ValidSnowflake(value string) bool {
	return snowflakePattern.MatchString(value)
}

type Policy struct {
	guildID           string
	allowedChannelIDs map[string]struct{}
	allowedThreadIDs  map[string]struct{}
}

type DiscordURL struct {
	GuildID   string
	ChannelID string
	MessageID string
}

func New(guildID string, allowedChannelIDs, allowedThreadIDs []string) Policy {
	return Policy{
		guildID:           guildID,
		allowedChannelIDs: toSet(allowedChannelIDs),
		allowedThreadIDs:  toSet(allowedThreadIDs),
	}
}

func (p Policy) AuthorizeGuild(guildID string) error {
	if guildID != p.guildID {
		return errors.New("guild is not authorized")
	}
	return nil
}

func (p Policy) HasExplicitThreadRestrictions() bool {
	return len(p.allowedThreadIDs) != 0
}

func (p Policy) AuthorizeChannel(guildID, channelID string) error {
	if err := p.AuthorizeGuild(guildID); err != nil {
		return err
	}
	if _, allowed := p.allowedChannelIDs[channelID]; !allowed {
		return errors.New("channel is not authorized")
	}
	return nil
}

func (p Policy) AuthorizeThread(guildID, threadID, parentChannelID string) error {
	if err := p.AuthorizeChannel(guildID, parentChannelID); err != nil {
		return err
	}
	if len(p.allowedThreadIDs) == 0 {
		return nil
	}
	if _, allowed := p.allowedThreadIDs[threadID]; !allowed {
		return errors.New("thread is not authorized")
	}
	return nil
}

func ParseDiscordURL(raw string) (DiscordURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return DiscordURL{}, errors.New("invalid Discord URL")
	}
	if parsed.Scheme != "https" || parsed.Host != "discord.com" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return DiscordURL{}, errors.New("unsupported Discord URL")
	}

	parts := strings.Split(parsed.Path, "/")
	if (len(parts) != 4 && len(parts) != 5) || parts[0] != "" || parts[1] != "channels" {
		return DiscordURL{}, errors.New("unsupported Discord URL path")
	}
	for _, id := range parts[2:] {
		if !ValidSnowflake(id) {
			return DiscordURL{}, errors.New("Discord URL identifiers must be snowflakes")
		}
	}

	result := DiscordURL{GuildID: parts[2], ChannelID: parts[3]}
	if len(parts) == 5 {
		result.MessageID = parts[4]
	}
	return result, nil
}

func toSet(ids []string) map[string]struct{} {
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}
