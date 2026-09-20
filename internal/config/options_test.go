package config

import (
	"math/bits"
	"regexp"
	"testing"
)

func TestLanguagesTableCompleteness(t *testing.T) {
	codes := make(map[string]bool, len(Languages))
	ids := make(map[uint32]bool, len(Languages))
	for _, o := range Languages {
		codes[o.Code] = true
		ids[o.ID] = true
		if o.ID == 0 || bits.OnesCount32(o.ID) != 1 {
			t.Errorf("language %q has non-single-bit ID 0x%x", o.Code, o.ID)
		}
	}
	if len(codes) != len(Languages) {
		t.Errorf("language codes not unique: got %d unique of %d", len(codes), len(Languages))
	}
	if len(ids) != len(Languages) {
		t.Errorf("language IDs not unique: got %d unique of %d", len(ids), len(Languages))
	}
}

func TestLanguagesRegexp(t *testing.T) {
	find := func(code string) Option {
		for _, o := range Languages {
			if o.Code == code {
				return o
			}
		}
		t.Fatalf("language code %q not found", code)
		return Option{}
	}
	cases := []struct {
		code  string
		match []string
		miss  []string
	}{
		{"en", []string{"en", "eng", "english", "en_US"}, []string{"de"}},
		{"cn", []string{"cn", "zh", "zho", "chinese", "zh_CN", "zh-CN", "zh-Hans"}, []string{"en", "ja"}},
		{"no", []string{"no", "nor", "norwegian", "nb_no", "nn-NO"}, []string{"de"}},
		{"es_mx", []string{"es_mx", "es-mx", "es-419"}, []string{"es"}},
	}
	for _, c := range cases {
		opt := find(c.code)
		re, err := regexp.Compile("^(" + opt.Regexp + ")$")
		if err != nil {
			t.Fatalf("language %q regexp %q does not compile: %v", c.code, opt.Regexp, err)
		}
		for _, s := range c.match {
			if !re.MatchString(s) {
				t.Errorf("language %q regexp should match %q", c.code, s)
			}
		}
		for _, s := range c.miss {
			if re.MatchString(s) {
				t.Errorf("language %q regexp should not match %q", c.code, s)
			}
		}
	}
}

// TestPlatformTableOrder pins the platform table's order, which is observable:
// util.OptionNameString renders a platform mask in table order, so this is the
// order the "Platforms:" line of the list output prints.
func TestPlatformTableOrder(t *testing.T) {
	want := []string{"win", "mac", "linux"}
	for i, code := range want {
		if i >= len(Platforms) {
			t.Fatalf("Platforms has %d entries, want at least %d", len(Platforms), len(want))
		}
		if Platforms[i].Code != code {
			t.Errorf("Platforms[%d].Code = %q, want %q", i, Platforms[i].Code, code)
		}
	}
}

func TestIncludeOptionsCompositeMasks(t *testing.T) {
	wantCodeID := map[string]uint32{
		"bi": GFBaseInstaller,
		"be": GFBaseExtra,
		"bp": GFBasePatch,
		"bl": GFBaseLangPack,
		"di": GFDLCInstaller,
		"de": GFDLCExtra,
		"dp": GFDLCPatch,
		"dl": GFDLCLangPack,
		"d":  GFDLC,
		"b":  GFBase,
		"i":  GFInstaller,
		"e":  GFExtra,
		"p":  GFPatch,
		"l":  GFLangPack,
	}
	for _, o := range IncludeOptions {
		if want, ok := wantCodeID[o.Code]; !ok || o.ID != want {
			t.Errorf("IncludeOptions code %q ID = 0x%x, want 0x%x", o.Code, o.ID, want)
		}
	}
	// Every documented entry must be in the table: the pair of loops pins the
	// set both ways without locking its size to a literal.
	if len(IncludeOptions) != len(wantCodeID) {
		t.Errorf("IncludeOptions has %d entries, want the %d documented above", len(IncludeOptions), len(wantCodeID))
	}
	// Composite masks must equal the union of their parts.
	if GFBase != GFBaseInstaller|GFBaseExtra|GFBasePatch|GFBaseLangPack|GFCustomBase {
		t.Errorf("GFBase composition mismatch: 0x%x", GFBase)
	}
	if GFDLC != GFDLCInstaller|GFDLCExtra|GFDLCPatch|GFDLCLangPack|GFCustomDLC {
		t.Errorf("GFDLC composition mismatch: 0x%x", GFDLC)
	}
	if GFInstaller != GFBaseInstaller|GFDLCInstaller {
		t.Errorf("GFInstaller composition mismatch: 0x%x", GFInstaller)
	}
	if GFLangPack != GFBaseLangPack|GFDLCLangPack {
		t.Errorf("GFLangPack composition mismatch: 0x%x", GFLangPack)
	}
	if GFCustom != GFCustomBase|GFCustomDLC {
		t.Errorf("GFCustom composition mismatch: 0x%x", GFCustom)
	}
}

// TestProtocolPrefix pins the deep-link scheme's exact value: it is the string
// the download layer strips from a file spec and the CLI documents in help, so
// the value is a contract with the gogdownloader:// URI format rather than an
// internal detail.
func TestProtocolPrefix(t *testing.T) {
	if ProtocolPrefix != "gogdownloader://" {
		t.Errorf("ProtocolPrefix = %q, want %q", ProtocolPrefix, "gogdownloader://")
	}
}
