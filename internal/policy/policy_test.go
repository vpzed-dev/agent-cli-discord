package policy

import "testing"

const (
	testGuild        = "12345678901234567"
	testOtherGuild   = "22345678901234567"
	testChannel      = "32345678901234567"
	testOtherChannel = "42345678901234567"
	testThread       = "52345678901234567"
	testOtherThread  = "62345678901234567"
	testMessage      = "72345678901234567"
)

func TestChannelAuthorizationRejectsOutsideIDsBeforeNetwork(t *testing.T) {
	policy := New(testGuild, []string{testChannel}, nil)

	if err := policy.AuthorizeChannel(testGuild, testChannel); err != nil {
		t.Fatalf("allowed channel rejected: %v", err)
	}
	for name, ids := range map[string][2]string{
		"other guild":   {testOtherGuild, testChannel},
		"other channel": {testGuild, testOtherChannel},
		"channel name":  {testGuild, "general"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := policy.AuthorizeChannel(ids[0], ids[1]); err == nil {
				t.Fatal("AuthorizeChannel() error = nil, want local rejection")
			}
		})
	}
}

func TestThreadAuthorizationRequiresVerifiedAllowedParent(t *testing.T) {
	policy := New(testGuild, []string{testChannel}, nil)

	if err := policy.AuthorizeThread(testGuild, testThread, testChannel); err != nil {
		t.Fatalf("thread under allowed parent rejected: %v", err)
	}
	if err := policy.AuthorizeThread(testGuild, testThread, testOtherChannel); err == nil {
		t.Fatal("thread under disallowed parent accepted")
	}
	if err := policy.AuthorizeThread(testOtherGuild, testThread, testChannel); err == nil {
		t.Fatal("thread in disallowed guild accepted")
	}
	if err := policy.AuthorizeThread(testGuild, testThread, ""); err == nil {
		t.Fatal("thread without a verified parent accepted")
	}
}

func TestExplicitThreadListNarrowsParentInheritance(t *testing.T) {
	policy := New(testGuild, []string{testChannel}, []string{testThread})

	if err := policy.AuthorizeThread(testGuild, testThread, testChannel); err != nil {
		t.Fatalf("explicitly allowed thread rejected: %v", err)
	}
	if err := policy.AuthorizeThread(testGuild, testOtherThread, testChannel); err == nil {
		t.Fatal("unlisted thread inherited access despite explicit thread restrictions")
	}
	if err := policy.AuthorizeThread(testGuild, testThread, testOtherChannel); err == nil {
		t.Fatal("listed thread bypassed parent channel restriction")
	}
}

func TestParseDiscordURLReturnsAuthorizationIdentifiers(t *testing.T) {
	tests := []struct {
		url       string
		messageID string
	}{
		{url: "https://discord.com/channels/" + testGuild + "/" + testChannel},
		{url: "https://discord.com/channels/" + testGuild + "/" + testChannel + "/" + testMessage, messageID: testMessage},
	}

	for _, test := range tests {
		got, err := ParseDiscordURL(test.url)
		if err != nil {
			t.Fatalf("ParseDiscordURL(%q) error = %v", test.url, err)
		}
		if got.GuildID != testGuild || got.ChannelID != testChannel || got.MessageID != test.messageID {
			t.Fatalf("ParseDiscordURL(%q) = %#v", test.url, got)
		}
	}
}

func TestDiscordURLUsesTheSameAuthorizationPolicy(t *testing.T) {
	policy := New(testGuild, []string{testChannel}, nil)
	parsed, err := ParseDiscordURL("https://discord.com/channels/" + testOtherGuild + "/" + testChannel)
	if err != nil {
		t.Fatalf("ParseDiscordURL() error = %v", err)
	}
	if err := policy.AuthorizeChannel(parsed.GuildID, parsed.ChannelID); err == nil {
		t.Fatal("URL for disallowed guild bypassed policy")
	}
}

func TestParseDiscordURLRejectsUnsupportedAndAmbiguousForms(t *testing.T) {
	tests := map[string]string{
		"non HTTPS":      "http://discord.com/channels/" + testGuild + "/" + testChannel,
		"alternate host": "https://canary.discord.com/channels/" + testGuild + "/" + testChannel,
		"invite":         "https://discord.com/invite/example",
		"DM":             "https://discord.com/channels/@me/" + testChannel,
		"guild name":     "https://discord.com/channels/my-guild/" + testChannel,
		"channel name":   "https://discord.com/channels/" + testGuild + "/general",
		"query":          "https://discord.com/channels/" + testGuild + "/" + testChannel + "?source=test",
		"fragment":       "https://discord.com/channels/" + testGuild + "/" + testChannel + "#message",
		"credentials":    "https://user@discord.com/channels/" + testGuild + "/" + testChannel,
		"port":           "https://discord.com:443/channels/" + testGuild + "/" + testChannel,
		"trailing slash": "https://discord.com/channels/" + testGuild + "/" + testChannel + "/",
		"extra segment":  "https://discord.com/channels/" + testGuild + "/" + testChannel + "/" + testMessage + "/extra",
		"encoded ID":     "https://discord.com/channels/%31" + testGuild[1:] + "/" + testChannel,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDiscordURL(raw); err == nil {
				t.Fatal("ParseDiscordURL() error = nil, want rejection")
			}
		})
	}
}
