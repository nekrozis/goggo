package config

import (
	"math/bits"
	"regexp"
	"testing"
)

func TestLanguagesTableCompleteness(t *testing.T) {
	if len(Languages) != 28 {
		t.Fatalf("len(Languages) = %d, want 28", len(Languages))
	}
	codes := make(map[string]bool, len(Languages))
	ids := make(map[uint32]bool, len(Languages))
	for _, o := range Languages {
		codes[o.Code] = true
		ids[o.ID] = true
		if o.ID == 0 || bits.OnesCount32(o.ID) != 1 {
			t.Errorf("language %q has non-single-bit ID 0x%x", o.Code, o.ID)
		}
	}
	if len(codes) != 28 {
		t.Errorf("language codes not unique: got %d unique", len(codes))
	}
	if len(ids) != 28 {
		t.Errorf("language IDs not unique: got %d unique", len(ids))
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

func TestPlatformAndArchTables(t *testing.T) {
	if len(Platforms) != 3 {
		t.Fatalf("len(Platforms) = %d, want 3", len(Platforms))
	}
	wantPlatformCodes := []string{"win", "mac", "linux"}
	for i, want := range wantPlatformCodes {
		if Platforms[i].Code != want {
			t.Errorf("Platforms[%d].Code = %q, want %q", i, Platforms[i].Code, want)
		}
	}
	if len(GalaxyArchs) != 2 {
		t.Fatalf("len(GalaxyArchs) = %d, want 2", len(GalaxyArchs))
	}
	if GalaxyArchs[0].ID != ArchX86 || GalaxyArchs[1].ID != ArchX64 {
		t.Errorf("GalaxyArchs IDs wrong: %x, %x", GalaxyArchs[0].ID, GalaxyArchs[1].ID)
	}
}

func TestListFormatTable(t *testing.T) {
	if len(ListFormats) != 7 {
		t.Fatalf("len(ListFormats) = %d, want 7", len(ListFormats))
	}
	wantCodes := []string{"games", "details", "json", "tags", "transform", "userdata", "wishlist"}
	for i, want := range wantCodes {
		if ListFormats[i].Code != want {
			t.Errorf("ListFormats[%d].Code = %q, want %q", i, ListFormats[i].Code, want)
		}
	}
}

func TestIncludeOptionsCompositeMasks(t *testing.T) {
	if len(IncludeOptions) != 14 {
		t.Fatalf("len(IncludeOptions) = %d, want 14", len(IncludeOptions))
	}
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

func TestUnitConstants(t *testing.T) {
	if UnitFormatIEC != 1 || UnitFormatSI != 2 {
		t.Errorf("unit format values wrong: IEC=%d SI=%d", UnitFormatIEC, UnitFormatSI)
	}
	if UnitDivisorMIEC != 1024*1024 || UnitDivisorMSI != 1000*1000 {
		t.Errorf("unit divisors wrong")
	}
	if UnitStringMIEC != "MiB" || UnitStringMSI != "MB" {
		t.Errorf("unit strings wrong")
	}
}

func TestCacheAndProtocolConstants(t *testing.T) {
	if GameDetailsCacheVersion != 7 {
		t.Errorf("GameDetailsCacheVersion = %d, want 7", GameDetailsCacheVersion)
	}
	if ZlibWindowSize != 15 {
		t.Errorf("ZlibWindowSize = %d, want 15", ZlibWindowSize)
	}
	if ProtocolPrefix != "gogdownloader://" {
		t.Errorf("ProtocolPrefix = %q", ProtocolPrefix)
	}
}
