package credential

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	EnvironmentVariable = "DISCORD_BOT_TOKEN"
	maxTokenFileBytes   = 1024
)

type Source string

const (
	SourceEnvironment Source = "environment"
	SourceFile        Source = "file"
)

type Options struct {
	LookupEnv           func(string) (string, bool)
	ConfiguredTokenFile string
	UserConfigDir       string
}

type Result struct {
	Token    string
	Source   Source
	Path     string
	Warnings []string
}

func DefaultPath(userConfigDir string) string {
	return filepath.Join(userConfigDir, "agent-cli-discord", "token.env")
}

func Load(options Options) (Result, error) {
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if token, present := lookupEnv(EnvironmentVariable); present {
		if token == "" {
			return Result{}, fmt.Errorf("%s is set but empty", EnvironmentVariable)
		}
		return Result{Token: token, Source: SourceEnvironment}, nil
	}

	path := options.ConfiguredTokenFile
	if path == "" {
		userConfigDir := options.UserConfigDir
		if userConfigDir == "" {
			var err error
			userConfigDir, err = os.UserConfigDir()
			if err != nil {
				return Result{}, fmt.Errorf("resolve user configuration directory: %w", err)
			}
		}
		path = DefaultPath(userConfigDir)
	}

	file, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open token file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("inspect token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("token file must be a regular file")
	}
	warning, err := checkPermissions(runtime.GOOS, info.Mode().Perm())
	if err != nil {
		return Result{}, err
	}

	token, err := Parse(file)
	if err != nil {
		return Result{}, fmt.Errorf("parse token file: %w", err)
	}
	result := Result{Token: token, Source: SourceFile, Path: path}
	if warning != "" {
		result.Warnings = []string{warning}
	}
	return result, nil
}

func Parse(source io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(source, maxTokenFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	if len(raw) > maxTokenFileBytes {
		return "", fmt.Errorf("token file exceeds %d bytes", maxTokenFileBytes)
	}
	if !utf8.Valid(raw) {
		return "", errors.New("token file must be valid UTF-8")
	}

	var token string
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != EnvironmentVariable {
			return "", fmt.Errorf("unsupported assignment on line %d", lineNumber)
		}
		if found {
			return "", fmt.Errorf("duplicate %s assignment", EnvironmentVariable)
		}

		parsed, err := parseValue(strings.TrimSpace(value))
		if err != nil {
			return "", fmt.Errorf("invalid %s assignment on line %d", EnvironmentVariable, lineNumber)
		}
		token = parsed
		found = true
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan token file: %w", err)
	}
	if !found {
		return "", fmt.Errorf("token file does not contain %s", EnvironmentVariable)
	}
	return token, nil
}

func parseValue(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty token")
	}
	if value[0] == '\'' || value[0] == '"' {
		quote := value[0]
		if len(value) < 2 || value[len(value)-1] != quote {
			return "", errors.New("unclosed quoted token")
		}
		value = value[1 : len(value)-1]
		if value == "" || strings.ContainsRune(value, rune(quote)) || strings.ContainsAny(value, "\\$") {
			return "", errors.New("unsupported quoted token syntax")
		}
		return value, nil
	}
	if strings.ContainsAny(value, " \t#\\$'\"") {
		return "", errors.New("unsupported unquoted token syntax")
	}
	return value, nil
}

func checkPermissions(goos string, mode os.FileMode) (string, error) {
	if goos == "windows" {
		return "token file permissions could not be verified on this platform", nil
	}
	if mode.Perm()&0o022 != 0 {
		return "", errors.New("token file must not be writable by group or other users")
	}
	if mode.Perm()&0o044 != 0 {
		return "token file is readable by group or other users", nil
	}
	return "", nil
}
