package credential

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "test-token-value-that-must-never-appear-in-errors"

func TestLoadPrefersEnvironment(t *testing.T) {
	path := writeTokenFile(t, "DISCORD_BOT_TOKEN=file-token\n", 0o600)

	got, err := Load(Options{
		LookupEnv: func(key string) (string, bool) {
			if key != EnvironmentVariable {
				t.Fatalf("environment lookup key = %q", key)
			}
			return testToken, true
		},
		ConfiguredTokenFile: path,
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Token != testToken || got.Source != SourceEnvironment {
		t.Fatalf("result = %#v", got)
	}
}

func TestLoadUsesConfiguredThenDefaultTokenFile(t *testing.T) {
	userConfigDir := t.TempDir()
	defaultPath := DefaultPath(userConfigDir)
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath, []byte("DISCORD_BOT_TOKEN=default-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuredPath := writeTokenFile(t, "DISCORD_BOT_TOKEN=configured-token\n", 0o600)

	lookupMissing := func(string) (string, bool) { return "", false }
	configured, err := Load(Options{LookupEnv: lookupMissing, ConfiguredTokenFile: configuredPath, UserConfigDir: userConfigDir})
	if err != nil {
		t.Fatalf("configured Load() error = %v", err)
	}
	if configured.Token != "configured-token" || configured.Path != configuredPath {
		t.Fatalf("configured result = %#v", configured)
	}

	fallback, err := Load(Options{LookupEnv: lookupMissing, UserConfigDir: userConfigDir})
	if err != nil {
		t.Fatalf("default Load() error = %v", err)
	}
	if fallback.Token != "default-token" || fallback.Path != defaultPath {
		t.Fatalf("default result = %#v", fallback)
	}
}

func TestParseDotenvAcceptedGrammar(t *testing.T) {
	tests := map[string]string{
		"plain":         "DISCORD_BOT_TOKEN=" + testToken,
		"whitespace":    "  DISCORD_BOT_TOKEN = " + testToken + "  \n",
		"single quoted": "DISCORD_BOT_TOKEN='" + testToken + "'\n",
		"double quoted": "# credential\r\n\r\nDISCORD_BOT_TOKEN=\"" + testToken + "\"\r\n",
		"no final LF":   "# credential\nDISCORD_BOT_TOKEN=" + testToken,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(raw))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got != testToken {
				t.Fatalf("token = %q, want test token", got)
			}
		})
	}
}

func TestParseDotenvRejectsUnsupportedOrMalformedInput(t *testing.T) {
	tests := map[string]string{
		"missing":            "# no token\n",
		"empty":              "DISCORD_BOT_TOKEN=\n",
		"empty quoted":       "DISCORD_BOT_TOKEN=\"\"\n",
		"duplicate":          "DISCORD_BOT_TOKEN=one\nDISCORD_BOT_TOKEN=two\n",
		"unrelated variable": "OTHER=value\nDISCORD_BOT_TOKEN=token\n",
		"export":             "export DISCORD_BOT_TOKEN=token\n",
		"interpolation":      "DISCORD_BOT_TOKEN=${TOKEN}\n",
		"inline comment":     "DISCORD_BOT_TOKEN=token # comment\n",
		"unclosed quote":     "DISCORD_BOT_TOKEN=\"token\n",
		"quoted suffix":      "DISCORD_BOT_TOKEN=\"token\"suffix\n",
		"escape":             "DISCORD_BOT_TOKEN=token\\\\nvalue\n",
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(raw)); err == nil {
				t.Fatal("Parse() error = nil, want rejection")
			}
		})
	}
}

func TestParseRejectsOversizedFile(t *testing.T) {
	raw := "#" + strings.Repeat("x", maxTokenFileBytes) + "\nDISCORD_BOT_TOKEN=token\n"
	if _, err := Parse(strings.NewReader(raw)); err == nil {
		t.Fatal("Parse() error = nil, want oversized-file rejection")
	}
}

func TestTokenFilePermissionPolicy(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		mode        os.FileMode
		wantWarning bool
		wantError   bool
	}{
		{name: "owner only", goos: "linux", mode: 0o600},
		{name: "group readable", goos: "linux", mode: 0o640, wantWarning: true},
		{name: "world readable", goos: "linux", mode: 0o604, wantWarning: true},
		{name: "group writable", goos: "linux", mode: 0o620, wantError: true},
		{name: "world writable", goos: "linux", mode: 0o602, wantError: true},
		{name: "unverifiable platform", goos: "windows", mode: 0o600, wantWarning: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			warning, err := checkPermissions(test.goos, test.mode)
			if (warning != "") != test.wantWarning {
				t.Fatalf("warning = %q, wantWarning = %t", warning, test.wantWarning)
			}
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %t", err, test.wantError)
			}
		})
	}
}

func TestErrorsAndWarningsNeverContainToken(t *testing.T) {
	path := writeTokenFile(t, "DISCORD_BOT_TOKEN="+testToken+"\n", 0o604)
	got, err := Load(Options{LookupEnv: func(string) (string, bool) { return "", false }, ConfiguredTokenFile: path})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(strings.Join(got.Warnings, "\n"), testToken) {
		t.Fatal("warning contains token")
	}

	_, err = Parse(strings.NewReader("DISCORD_BOT_TOKEN=" + testToken + " # unsupported\n"))
	if err == nil {
		t.Fatal("Parse() error = nil")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatal("error contains token")
	}
}

func writeTokenFile(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token.env")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	return path
}
