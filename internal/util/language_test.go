package util

import "testing"

func TestNormalizeLanguage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"zh-Hant", "zh-hant"},
		{"ZH_hant", "zh-hant"},
		{"en-US", "en-us"},
		{"en_US", "en-us"},
		{"ENG", "eng"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeLanguage(c.in); got != c.want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLanguageFamilyMatch(t *testing.T) {
	cases := []struct {
		request, candidate string
		want               bool
	}{
		{"en", "en-US", true},
		{"en", "en-GB", true},
		{"EN", "en-us", true},
		{"en_US", "en-US", true},
		{"en", "eng", true},
		{"en", "english", true},
		{"en", "de-DE", false},
		{"en-US", "en-US", true},
		{"en-US", "en-GB", false},
		{"zh", "zh-Hans", true},
		{"zh", "zh-Hant", true},
		{"zh-Hant", "zh-Hant", true},
		{"zh-Hant", "zh-Hans", false},
		{"fr", "fr-FR", true},
		// A bare prefix is forgiving of the non-BCP-47 spellings a manifest may
		// declare, but only where the spelling starts with the request: "french"
		// answers "fr", "german" does not answer "de".
		{"fr", "french", true},
		{"de", "german", false},
		{"de", "deu", true},
		{"", "en-US", false},
		{"klingon", "en-US", false},
	}
	for _, c := range cases {
		if got := LanguageFamilyMatch(c.request, c.candidate); got != c.want {
			t.Errorf("LanguageFamilyMatch(%q, %q) = %v, want %v", c.request, c.candidate, got, c.want)
		}
	}
	// An empty request must not match by being a prefix of everything.
	if LanguageFamilyMatch("", "") {
		t.Error("LanguageFamilyMatch(\"\", \"\") must be false")
	}
}
