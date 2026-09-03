package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

const (
	currentSchemaVersion  = "1"
	defaultRequestTimeout = 15 * time.Second
	defaultCommandTimeout = 30 * time.Second
	maxConfigBytes        = 1 << 20
)

var snowflakePattern = regexp.MustCompile(`^[0-9]{17,20}$`)

type Config struct {
	SchemaVersion     string
	GuildID           string
	AllowedChannelIDs []string
	AllowedThreadIDs  []string
	RequestTimeout    time.Duration
	CommandTimeout    time.Duration
	TokenFile         string
	Log               *LogConfig
}

type LogConfig struct {
	Path  string
	Level string
}

type fileConfig struct {
	SchemaVersion     string         `json:"schema_version"`
	GuildID           string         `json:"guild_id"`
	AllowedChannelIDs []string       `json:"allowed_channel_ids"`
	AllowedThreadIDs  []string       `json:"allowed_thread_ids,omitempty"`
	RequestTimeout    string         `json:"request_timeout,omitempty"`
	CommandTimeout    string         `json:"command_timeout,omitempty"`
	TokenFile         string         `json:"token_file,omitempty"`
	Log               *fileLogConfig `json:"log,omitempty"`
}

type fileLogConfig struct {
	Path  string `json:"path"`
	Level string `json:"level,omitempty"`
}

func Path(userConfigDir string) string {
	return filepath.Join(userConfigDir, "agent-cli-discord", "config.json")
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("inspect configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Config{}, errors.New("configuration must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return Config{}, errors.New("configuration must not be writable by group or other users")
	}

	config, err := Decode(file)
	if err != nil {
		return Config{}, fmt.Errorf("load configuration: %w", err)
	}
	return config, nil
}

func Decode(source io.Reader) (Config, error) {
	raw, err := io.ReadAll(io.LimitReader(source, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(raw) > maxConfigBytes {
		return Config{}, fmt.Errorf("configuration exceeds %d bytes", maxConfigBytes)
	}
	if err := rejectDuplicateFields(raw); err != nil {
		return Config{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file fileConfig
	if err := decoder.Decode(&file); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Config{}, err
	}

	return validate(file)
}

func Encode(destination io.Writer, config Config) error {
	if err := validateRuntime(config); err != nil {
		return err
	}

	file := fileConfig{
		SchemaVersion:     config.SchemaVersion,
		GuildID:           config.GuildID,
		AllowedChannelIDs: config.AllowedChannelIDs,
		AllowedThreadIDs:  config.AllowedThreadIDs,
		RequestTimeout:    config.RequestTimeout.String(),
		CommandTimeout:    config.CommandTimeout.String(),
		TokenFile:         config.TokenFile,
	}
	if config.Log != nil {
		file.Log = &fileLogConfig{Path: config.Log.Path, Level: config.Log.Level}
	}

	encoder := json.NewEncoder(destination)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	return nil
}

func validate(file fileConfig) (Config, error) {
	if file.SchemaVersion != currentSchemaVersion {
		return Config{}, fmt.Errorf("unsupported schema_version %q", file.SchemaVersion)
	}
	if !snowflakePattern.MatchString(file.GuildID) {
		return Config{}, errors.New("guild_id must be a 17-20 digit Discord snowflake")
	}
	if len(file.AllowedChannelIDs) == 0 {
		return Config{}, errors.New("allowed_channel_ids must contain at least one channel ID")
	}
	if err := validateIDs("allowed_channel_ids", file.AllowedChannelIDs); err != nil {
		return Config{}, err
	}
	if err := validateIDs("allowed_thread_ids", file.AllowedThreadIDs); err != nil {
		return Config{}, err
	}

	requestTimeout, err := parseTimeout("request_timeout", file.RequestTimeout, defaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}
	commandTimeout, err := parseTimeout("command_timeout", file.CommandTimeout, defaultCommandTimeout)
	if err != nil {
		return Config{}, err
	}
	if commandTimeout < requestTimeout {
		return Config{}, errors.New("command_timeout must be greater than or equal to request_timeout")
	}

	config := Config{
		SchemaVersion:     file.SchemaVersion,
		GuildID:           file.GuildID,
		AllowedChannelIDs: file.AllowedChannelIDs,
		AllowedThreadIDs:  file.AllowedThreadIDs,
		RequestTimeout:    requestTimeout,
		CommandTimeout:    commandTimeout,
		TokenFile:         file.TokenFile,
	}
	if file.Log != nil {
		level := file.Log.Level
		if level == "" {
			level = "info"
		}
		if file.Log.Path == "" {
			return Config{}, errors.New("log.path must not be empty when logging is enabled")
		}
		if !validLogLevel(level) {
			return Config{}, fmt.Errorf("unsupported log.level %q", level)
		}
		config.Log = &LogConfig{Path: file.Log.Path, Level: level}
	}
	return config, nil
}

func validateRuntime(config Config) error {
	file := fileConfig{
		SchemaVersion:     config.SchemaVersion,
		GuildID:           config.GuildID,
		AllowedChannelIDs: config.AllowedChannelIDs,
		AllowedThreadIDs:  config.AllowedThreadIDs,
		RequestTimeout:    config.RequestTimeout.String(),
		CommandTimeout:    config.CommandTimeout.String(),
		TokenFile:         config.TokenFile,
	}
	if config.Log != nil {
		file.Log = &fileLogConfig{Path: config.Log.Path, Level: config.Log.Level}
	}
	_, err := validate(file)
	return err
}

func validateIDs(field string, ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !snowflakePattern.MatchString(id) {
			return fmt.Errorf("%s must contain only 17-20 digit Discord snowflakes", field)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%s contains duplicate ID %q", field, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func parseTimeout(field, value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", field)
	}
	return duration, nil
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func rejectDuplicateFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanValue(decoder); err != nil {
		return fmt.Errorf("decode configuration: %w", err)
	}
	return requireEOF(decoder)
}

func scanValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = struct{}{}
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("configuration must contain exactly one JSON value")
	}
	return fmt.Errorf("decode trailing configuration data: %w", err)
}
