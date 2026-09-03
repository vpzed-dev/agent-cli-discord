package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vpzed-dev/agent-cli-discord/internal/audit"
	"github.com/vpzed-dev/agent-cli-discord/internal/config"
	"github.com/vpzed-dev/agent-cli-discord/internal/credential"
	"github.com/vpzed-dev/agent-cli-discord/internal/discord"
	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

const (
	executableName       = "agent-cli-discord"
	schemaVersion        = "1"
	version              = "dev"
	maxMessageInputBytes = 8000
)

type successEnvelope struct {
	OK       bool     `json:"ok"`
	Data     any      `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
}

type failureEnvelope struct {
	OK    bool       `json:"ok"`
	Error errorValue `json:"error"`
}

type errorValue struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Retryable      bool   `json:"retryable"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	DiscordCode    int    `json:"discord_code,omitempty"`
	RateLimitScope string `json:"rate_limit_scope,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

type versionData struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	SchemaVersion string `json:"schema_version"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type runtimeOptions struct {
	UserConfigDir string
	LookupEnv     func(string) (string, bool)
	Discord       discord.Options
	Input         io.Reader
	AuditCommand  string
	AuditIDs      auditIdentifiers
}

type auditIdentifiers struct {
	ChannelID string
	MessageID string
	ThreadID  string
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithOptions(args, stdout, stderr, runtimeOptions{})
}

func runWithOptions(args []string, stdout, stderr io.Writer, options runtimeOptions) int {
	if len(args) == 0 {
		return writeError(stderr, "cli.invalid_arguments", "a command is required")
	}
	options.AuditCommand, options.AuditIDs = auditMetadata(args)

	switch args[0] {
	case "version":
		if len(args) != 1 {
			return writeError(stderr, "cli.invalid_arguments", "version accepts no arguments")
		}
		return writeJSON(stdout, successEnvelope{
			OK: true,
			Data: versionData{
				Name:          executableName,
				Version:       version,
				SchemaVersion: schemaVersion,
			},
		})
	case "auth":
		if len(args) != 2 || args[1] != "check" {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord auth check")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, _ config.Config, _ policy.Policy) (any, error) {
			return client.AuthCheck(ctx)
		})
	case "channels":
		if len(args) != 2 || args[1] != "list" {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord channels list")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.ChannelsList(ctx, cfg.GuildID, access)
		})
	case "messages":
		return runMessagesCommand(args[1:], stdout, stderr, options)
	case "reactions":
		return runReactionsCommand(args[1:], stdout, stderr, options)
	case "threads":
		return runThreadsCommand(args[1:], stdout, stderr, options)
	default:
		return writeError(stderr, "cli.unknown_command", fmt.Sprintf("unknown command: %s", args[0]))
	}
}

func auditMetadata(args []string) (string, auditIdentifiers) {
	command := args[0]
	if len(args) > 1 {
		command += " " + args[1]
	}
	var identifiers auditIdentifiers
	for index := 2; index+1 < len(args); index += 2 {
		value := args[index+1]
		if !policy.ValidSnowflake(value) {
			continue
		}
		switch args[index] {
		case "--channel":
			identifiers.ChannelID = value
		case "--message":
			identifiers.MessageID = value
		case "--thread":
			identifiers.ThreadID = value
		}
	}
	return command, identifiers
}

type threadMembershipResult struct {
	ThreadID string `json:"thread_id"`
	Action   string `json:"action"`
}

func runThreadsCommand(args []string, stdout, stderr io.Writer, options runtimeOptions) int {
	if len(args) == 0 {
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord threads list|create|join|leave [options]")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord threads list")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.ThreadsList(ctx, cfg.GuildID, access)
		})
	case "create":
		values, err := parseOptions(args[1:], map[string]bool{"--channel": true, "--name": true, "--auto-archive": true})
		if err != nil || !policy.ValidSnowflake(values["--channel"]) || !validThreadName(values["--name"]) {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord threads create --channel PARENT_ID --name NAME [--auto-archive MINUTES]")
		}
		autoArchive := 1440
		if raw := values["--auto-archive"]; raw != "" {
			autoArchive, err = strconv.Atoi(raw)
			if err != nil || !validThreadAutoArchive(autoArchive) {
				return writeError(stderr, "cli.invalid_arguments", "--auto-archive must be 60, 1440, 4320, or 10080")
			}
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.ThreadCreate(ctx, cfg.GuildID, values["--channel"], values["--name"], autoArchive, access)
		})
	case "join", "leave":
		action := args[0]
		values, err := parseOptions(args[1:], map[string]bool{"--thread": true})
		if err != nil || !policy.ValidSnowflake(values["--thread"]) {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord threads join|leave --thread ID")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			var operationErr error
			if action == "join" {
				operationErr = client.ThreadJoin(ctx, cfg.GuildID, values["--thread"], access)
			} else {
				operationErr = client.ThreadLeave(ctx, cfg.GuildID, values["--thread"], access)
			}
			if operationErr != nil {
				return nil, operationErr
			}
			return threadMembershipResult{ThreadID: values["--thread"], Action: action}, nil
		})
	default:
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord threads list|create|join|leave [options]")
	}
}

func validThreadName(name string) bool {
	length := utf8.RuneCountInString(name)
	return utf8.ValidString(name) && length >= 1 && length <= 100
}

func validThreadAutoArchive(value int) bool {
	return value == 60 || value == 1440 || value == 4320 || value == 10080
}

type reactionResult struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
	Action    string `json:"action"`
}

func runReactionsCommand(args []string, stdout, stderr io.Writer, options runtimeOptions) int {
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord reactions add|remove --channel ID --message ID --emoji EMOJI")
	}
	action := args[0]
	values, err := parseOptions(args[1:], map[string]bool{"--channel": true, "--message": true, "--emoji": true})
	if err != nil || !policy.ValidSnowflake(values["--channel"]) || !policy.ValidSnowflake(values["--message"]) || !discord.ValidReactionEmoji(values["--emoji"]) {
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord reactions add|remove --channel ID --message ID --emoji EMOJI")
	}
	return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
		var operationErr error
		if action == "add" {
			operationErr = client.ReactionAdd(ctx, cfg.GuildID, values["--channel"], values["--message"], values["--emoji"], access)
		} else {
			operationErr = client.ReactionRemove(ctx, cfg.GuildID, values["--channel"], values["--message"], values["--emoji"], access)
		}
		if operationErr != nil {
			return nil, operationErr
		}
		return reactionResult{ChannelID: values["--channel"], MessageID: values["--message"], Emoji: values["--emoji"], Action: action}, nil
	})
}

func runMessagesCommand(args []string, stdout, stderr io.Writer, options runtimeOptions) int {
	if len(args) == 0 {
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord messages read|get [options]")
	}
	switch args[0] {
	case "read":
		channelID, readOptions, err := parseMessagesRead(args[1:])
		if err != nil {
			return writeError(stderr, "cli.invalid_arguments", err.Error())
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.MessagesRead(ctx, cfg.GuildID, channelID, access, readOptions)
		})
	case "get":
		values, err := parseOptions(args[1:], map[string]bool{"--channel": true, "--message": true})
		if err != nil || values["--channel"] == "" || values["--message"] == "" || !policy.ValidSnowflake(values["--channel"]) || !policy.ValidSnowflake(values["--message"]) {
			return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord messages get --channel ID --message ID")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.MessageGet(ctx, cfg.GuildID, values["--channel"], values["--message"], access)
		})
	case "post", "reply":
		writeOptions, err := parseMessageWrite(args[0], args[1:])
		if err != nil {
			return writeError(stderr, "cli.invalid_arguments", err.Error())
		}
		content, err := loadMessageContent(writeOptions.file, options.Input)
		if err != nil {
			return writeError(stderr, "cli.invalid_arguments", err.Error())
		}
		if content == "" && len(writeOptions.attachments) == 0 {
			return writeError(stderr, "cli.invalid_arguments", "message content must not be empty without an attachment")
		}
		return runDiscordCommand(stdout, stderr, options, func(ctx context.Context, client *discord.Client, cfg config.Config, access policy.Policy) (any, error) {
			return client.MessagePost(ctx, cfg.GuildID, writeOptions.channelID, access, discord.PostOptions{Content: content, ReplyTo: writeOptions.messageID, AttachmentPaths: writeOptions.attachments})
		})
	default:
		return writeError(stderr, "cli.invalid_arguments", "usage: agent-cli-discord messages read|get|post|reply [options]")
	}
}

type messageWriteOptions struct {
	channelID   string
	messageID   string
	file        string
	attachments []string
}

func parseMessageWrite(command string, args []string) (messageWriteOptions, error) {
	if len(args)%2 != 0 {
		return messageWriteOptions{}, errors.New("every option requires a value")
	}
	var result messageWriteOptions
	seen := make(map[string]bool)
	for index := 0; index < len(args); index += 2 {
		name, value := args[index], args[index+1]
		if strings.HasPrefix(value, "--") {
			return messageWriteOptions{}, fmt.Errorf("option %q requires a value", name)
		}
		if name != "--attach" && seen[name] {
			return messageWriteOptions{}, fmt.Errorf("duplicate option %q", name)
		}
		seen[name] = true
		switch name {
		case "--channel":
			result.channelID = value
		case "--message":
			result.messageID = value
		case "--file":
			result.file = value
		case "--attach":
			result.attachments = append(result.attachments, value)
		default:
			return messageWriteOptions{}, fmt.Errorf("unknown option %q", name)
		}
	}
	if !policy.ValidSnowflake(result.channelID) {
		return messageWriteOptions{}, errors.New("--channel must be a Discord snowflake")
	}
	if command == "reply" && !policy.ValidSnowflake(result.messageID) {
		return messageWriteOptions{}, errors.New("--message must be a Discord snowflake for replies")
	}
	if command == "post" && result.messageID != "" {
		return messageWriteOptions{}, errors.New("--message is only valid for replies")
	}
	if len(result.attachments) > 10 {
		return messageWriteOptions{}, errors.New("a message may contain at most 10 attachments")
	}
	return result, nil
}

func loadMessageContent(path string, input io.Reader) (string, error) {
	var source io.Reader = input
	var file *os.File
	if path != "" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return "", errors.New("could not open message content file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("message content file must be a regular file")
		}
		source = file
	}
	if source == nil {
		source = os.Stdin
	}
	raw, err := io.ReadAll(io.LimitReader(source, maxMessageInputBytes+1))
	if err != nil {
		return "", errors.New("could not read message content")
	}
	if len(raw) > maxMessageInputBytes {
		return "", errors.New("message content exceeds 8000 bytes")
	}
	if !utf8.Valid(raw) {
		return "", errors.New("message content must be valid UTF-8")
	}
	if utf8.RuneCount(raw) > 2000 {
		return "", errors.New("message content exceeds 2000 characters")
	}
	return string(raw), nil
}

func parseMessagesRead(args []string) (string, discord.ReadOptions, error) {
	values, err := parseOptions(args, map[string]bool{"--channel": true, "--limit": true, "--before": true, "--after": true, "--around": true})
	if err != nil {
		return "", discord.ReadOptions{}, err
	}
	channelID := values["--channel"]
	if !policy.ValidSnowflake(channelID) {
		return "", discord.ReadOptions{}, errors.New("--channel must be a Discord snowflake")
	}
	result := discord.ReadOptions{Before: values["--before"], After: values["--after"], Around: values["--around"]}
	if rawLimit := values["--limit"]; rawLimit != "" {
		result.Limit, err = strconv.Atoi(rawLimit)
		if err != nil || result.Limit < 1 || result.Limit > 100 {
			return "", discord.ReadOptions{}, errors.New("--limit must be an integer between 1 and 100")
		}
	}
	cursors := 0
	for _, value := range []string{result.Before, result.After, result.Around} {
		if value != "" {
			cursors++
			if !policy.ValidSnowflake(value) {
				return "", discord.ReadOptions{}, errors.New("cursor values must be Discord snowflakes")
			}
		}
	}
	if cursors > 1 {
		return "", discord.ReadOptions{}, errors.New("--before, --after, and --around are mutually exclusive")
	}
	return channelID, result, nil
}

func parseOptions(args []string, allowed map[string]bool) (map[string]string, error) {
	if len(args)%2 != 0 {
		return nil, errors.New("every option requires a value")
	}
	values := make(map[string]string, len(args)/2)
	for index := 0; index < len(args); index += 2 {
		name, value := args[index], args[index+1]
		if !allowed[name] || !strings.HasPrefix(name, "--") {
			return nil, fmt.Errorf("unknown option %q", name)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate option %q", name)
		}
		if strings.HasPrefix(value, "--") {
			return nil, fmt.Errorf("option %q requires a value", name)
		}
		values[name] = value
	}
	return values, nil
}

func runDiscordCommand(stdout, stderr io.Writer, options runtimeOptions, operation func(context.Context, *discord.Client, config.Config, policy.Policy) (any, error)) int {
	configDir := options.UserConfigDir
	if configDir == "" {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			return writeError(stderr, "config.unavailable", "could not resolve user configuration directory")
		}
	}
	cfg, err := config.Load(config.Path(configDir))
	if err != nil {
		return writeError(stderr, "config.invalid", err.Error())
	}
	var logger *audit.Logger
	if cfg.Log != nil {
		logger, err = audit.Open(cfg.Log.Path)
		if err != nil {
			return writeError(stderr, "log.unavailable", "could not open configured audit log")
		}
		defer logger.Close()
	}
	writeAudit := func(outcome string) error {
		if logger == nil {
			return nil
		}
		return logger.Append(audit.Event{
			SchemaVersion: schemaVersion,
			Timestamp:     time.Now().UTC(),
			Level:         cfg.Log.Level,
			Name:          "command.completed",
			Command:       options.AuditCommand,
			Outcome:       outcome,
			GuildID:       cfg.GuildID,
			ChannelID:     options.AuditIDs.ChannelID,
			MessageID:     options.AuditIDs.MessageID,
			ThreadID:      options.AuditIDs.ThreadID,
		})
	}
	credentialResult, err := credential.Load(credential.Options{
		LookupEnv:           options.LookupEnv,
		ConfiguredTokenFile: cfg.TokenFile,
		UserConfigDir:       configDir,
	})
	if err != nil {
		if writeAudit("failure") != nil {
			return writeError(stderr, "log.unavailable", "could not append to configured audit log")
		}
		return writeError(stderr, "credential.unavailable", err.Error())
	}
	discordOptions := options.Discord
	discordOptions.RequestTimeout = cfg.RequestTimeout
	client := discord.New(credentialResult.Token, discordOptions)
	access := policy.New(cfg.GuildID, cfg.AllowedChannelIDs, cfg.AllowedThreadIDs)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.CommandTimeout)
	defer cancel()
	result, err := operation(ctx, client, cfg, access)
	if err != nil {
		if writeAudit("failure") != nil {
			return writeError(stderr, "log.unavailable", "command failed and its audit event could not be recorded")
		}
		return writeCommandError(stderr, err)
	}
	if writeAudit("success") != nil {
		return writeError(stderr, "log.unavailable", "command completed but its audit event could not be recorded")
	}
	return writeJSON(stdout, successEnvelope{OK: true, Data: result, Warnings: credentialResult.Warnings})
}

func writeCommandError(stderr io.Writer, err error) int {
	var discordErr *discord.Error
	if errors.As(err, &discordErr) {
		return writeErrorValue(stderr, errorValue{
			Code: discordErr.Code, Message: discordErr.Message, Retryable: discordErr.Retryable,
			HTTPStatus: discordErr.HTTPStatus, DiscordCode: discordErr.DiscordCode,
			RateLimitScope: discordErr.RateLimitScope, OutcomeUnknown: discordErr.OutcomeUnknown,
		})
	}
	return writeError(stderr, "internal.error", "command failed")
}

func writeError(stderr io.Writer, code, message string) int {
	return writeErrorValue(stderr, errorValue{Code: code, Message: message})
}

func writeErrorValue(stderr io.Writer, value errorValue) int {
	if exitCode := writeJSON(stderr, failureEnvelope{
		OK:    false,
		Error: value,
	}); exitCode != 0 {
		return exitCode
	}
	return 2
}

func writeJSON(destination io.Writer, value any) int {
	if err := json.NewEncoder(destination).Encode(value); err != nil {
		return 1
	}
	return 0
}
