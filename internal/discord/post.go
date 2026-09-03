package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vpzed-dev/agent-cli-discord/internal/policy"
)

type PostOptions struct {
	Content         string
	ReplyTo         string
	AttachmentPaths []string
}

const (
	maxAttachments          = 10
	maxAttachmentBytes      = 10 << 20
	maxAggregateUploadBytes = 24 << 20
)

type createMessagePayload struct {
	Content          string                  `json:"content"`
	AllowedMentions  allowedMentions         `json:"allowed_mentions"`
	MessageReference *createMessageReference `json:"message_reference,omitempty"`
	Attachments      []createAttachment      `json:"attachments,omitempty"`
}

type createAttachment struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
}

type allowedMentions struct {
	Parse       []string `json:"parse"`
	RepliedUser bool     `json:"replied_user"`
}

type createMessageReference struct {
	MessageID       string `json:"message_id"`
	ChannelID       string `json:"channel_id"`
	GuildID         string `json:"guild_id"`
	FailIfNotExists bool   `json:"fail_if_not_exists"`
}

func (c *Client) MessagePost(ctx context.Context, guildID, channelID string, access policy.Policy, options PostOptions) (Message, error) {
	if err := c.authorizeMessageTarget(ctx, access, guildID, channelID); err != nil {
		return Message{}, err
	}
	if !utf8.ValidString(options.Content) {
		return Message{}, &Error{Code: "cli.invalid_arguments", Message: "message content must be valid UTF-8"}
	}
	if utf8.RuneCountInString(options.Content) > 2000 {
		return Message{}, &Error{Code: "cli.invalid_arguments", Message: "message content exceeds 2000 characters"}
	}
	if options.Content == "" && len(options.AttachmentPaths) == 0 {
		return Message{}, &Error{Code: "cli.invalid_arguments", Message: "message content must not be empty"}
	}
	if len(options.AttachmentPaths) > maxAttachments {
		return Message{}, &Error{Code: "cli.invalid_arguments", Message: "a message may contain at most 10 attachments"}
	}
	payload := createMessagePayload{
		Content:         options.Content,
		AllowedMentions: allowedMentions{Parse: []string{}, RepliedUser: false},
	}
	if options.ReplyTo != "" {
		if !policy.ValidSnowflake(options.ReplyTo) {
			return Message{}, &Error{Code: "cli.invalid_arguments", Message: "reply message ID must be a Discord snowflake"}
		}
		payload.MessageReference = &createMessageReference{
			MessageID: options.ReplyTo, ChannelID: channelID, GuildID: guildID, FailIfNotExists: true,
		}
	}
	files, err := openAttachments(options.AttachmentPaths, &payload)
	if err != nil {
		return Message{}, err
	}
	defer closeFiles(files)
	body, err := json.Marshal(payload)
	if err != nil {
		return Message{}, errors.New("encode message payload")
	}
	var created Message
	request := Request{Method: http.MethodPost, Path: "/channels/" + channelID + "/messages", JSONBody: body}
	if len(files) > 0 {
		request.JSONBody = nil
		request.Body, request.ContentType = multipartBody(body, files)
	}
	if err := c.Do(ctx, request, &created); err != nil {
		return Message{}, err
	}
	return created, nil
}

func openAttachments(paths []string, payload *createMessagePayload) ([]*os.File, error) {
	files := make([]*os.File, 0, len(paths))
	var total int64
	for index, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			closeFiles(files)
			return nil, &Error{Code: "attachment.unavailable", Message: "could not open attachment"}
		}
		info, err := file.Stat()
		name := filepath.Base(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxAttachmentBytes || strings.ContainsAny(name, "\r\n") {
			file.Close()
			closeFiles(files)
			return nil, &Error{Code: "attachment.invalid", Message: "attachment must be a safe regular file no larger than 10 MiB"}
		}
		total += info.Size()
		if total > maxAggregateUploadBytes {
			file.Close()
			closeFiles(files)
			return nil, &Error{Code: "attachment.too_large", Message: "combined attachments exceed the request size limit"}
		}
		files = append(files, file)
		payload.Attachments = append(payload.Attachments, createAttachment{ID: index, Filename: name})
	}
	return files, nil
}

func multipartBody(payload []byte, files []*os.File) (io.Reader, string) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	go func() {
		part, err := multipartWriter.CreateFormField("payload_json")
		if err == nil {
			_, err = part.Write(payload)
		}
		for index, file := range files {
			if err != nil {
				break
			}
			part, err = multipartWriter.CreateFormFile(fmt.Sprintf("files[%d]", index), filepath.Base(file.Name()))
			if err == nil {
				_, err = io.Copy(part, file)
			}
		}
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
	}()
	return reader, contentType
}

func closeFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
