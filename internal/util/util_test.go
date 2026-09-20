package util

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		in, sep string
		want    []string
	}{
		{"a,b,c", ",", []string{"a", "b", "c"}},
		{"a,b,", ",", []string{"a", "b"}},
		{"a,,b", ",", []string{"a", "b"}},
		{"", ",", nil},
		{",", ",", nil},
		{"a+b,c", "+", []string{"a", "b,c"}},
		{"x--y", "--", []string{"x", "y"}},
	}
	for _, c := range cases {
		if got := Split(c.in, c.sep); !equalStrings(got, c.want) {
			t.Errorf("Split(%q,%q) = %v, want %v", c.in, c.sep, got, c.want)
		}
	}
	if got := Split("a", ""); got != nil {
		t.Errorf("Split with empty separator = %v, want nil (guard)", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOptionValueAll(t *testing.T) {
	if got := OptionValue("all", config.Platforms, false); got != config.PlatformWindows|config.PlatformMac|config.PlatformLinux {
		t.Errorf("OptionValue(all) = 0x%x", got)
	}
}

func TestOptionValueIntegerConversion(t *testing.T) {
	if got := OptionValue("7", config.Platforms, true); got != 7 {
		t.Errorf("OptionValue(\"7\") = %d, want 7", got)
	}
	if got := OptionValue("123", config.Platforms, false); got != 0 {
		t.Errorf("OptionValue(\"123\", allowInt=false) = %d, want 0", got)
	}
	// A negative literal wraps through int32 into the unsigned mask.
	neg := int32(-5)
	if got := OptionValue("-5", config.Platforms, true); got != uint32(neg) {
		t.Errorf("OptionValue(\"-5\") = %d, want %d", got, uint32(neg))
	}
	// int32 boundary extremes.
	wrap := func(w int64) uint32 { return uint32(int32(w)) }
	extremes := []struct {
		in      string
		inRange bool
		want    uint32
	}{
		{"-1", true, wrap(-1)},
		{"-2147483648", true, wrap(-2147483648)},
		{"2147483647", true, wrap(2147483647)},
		// Out of int32 range: the value maps to 0 (no match).
		{"-2147483649", false, 0},
		{"2147483648", false, 0},
		{"99999999999999999999", false, 0},
	}
	for _, c := range extremes {
		if got := OptionValue(c.in, config.Platforms, true); got != c.want {
			t.Errorf("OptionValue(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	// A non-integer string like "+" must not be parsed.
	if got := OptionValue("+", config.Platforms, true); got != 0 {
		t.Errorf("OptionValue(\"+\") = %d, want 0", got)
	}
}

func TestOptionValueCodeAndRegexp(t *testing.T) {
	if got := OptionValue("win", config.Platforms, false); got != config.PlatformWindows {
		t.Errorf("OptionValue(win) = %d", got)
	}
	// Regexp alias, case-insensitive, whole match.
	if got := OptionValue("WiNdOwS", config.Platforms, false); got != config.PlatformWindows {
		t.Errorf("OptionValue(WiNdOwS) = %d", got)
	}
	if got := OptionValue("windowsx", config.Platforms, false); got != 0 {
		t.Errorf("OptionValue(windowsx) = %d, want 0 (whole match)", got)
	}
	if got := OptionValue("nope", config.Platforms, false); got != 0 {
		t.Errorf("OptionValue(nope) = %d, want 0", got)
	}
}

func TestOptionValueSequentialPriority(t *testing.T) {
	// First entry matches by Regexp before a later entry's Code would match,
	// proving per-entry sequential evaluation (3a before 3b per entry).
	opts := []config.Option{
		{ID: 1, Code: "z", Name: "first", Regexp: "b"},
		{ID: 2, Code: "b", Name: "second", Regexp: ""},
	}
	if got := OptionValue("b", opts, false); got != 1 {
		t.Errorf("OptionValue(b) = %d, want 1 (regexp of first entry wins)", got)
	}
}

func TestOptionNameString(t *testing.T) {
	got := OptionNameString(config.PlatformWindows|config.PlatformLinux, config.Platforms)
	if got != "Windows, Linux" {
		t.Errorf("OptionNameString = %q", got)
	}
	if got := OptionNameString(0, config.Platforms); got != "" {
		t.Errorf("OptionNameString(0) = %q, want empty", got)
	}
}

func TestParseOptionString(t *testing.T) {
	prio, mask := ParseOptionString("win+mac,linux", config.Platforms)
	wantMask := config.PlatformWindows | config.PlatformMac | config.PlatformLinux
	if mask != wantMask || len(prio) != 2 {
		t.Fatalf("mask=0x%x prio=%v", mask, prio)
	}
	if prio[0] != config.PlatformWindows|config.PlatformMac || prio[1] != config.PlatformLinux {
		t.Errorf("prio = %v", prio)
	}

	// Duplicates: within one "+" group they collapse; across "," groups they
	// are preserved.
	prio, mask = ParseOptionString("win+win", config.Platforms)
	if len(prio) != 1 || prio[0] != config.PlatformWindows || mask != config.PlatformWindows {
		t.Errorf("group dup: prio=%v mask=0x%x", prio, mask)
	}
	prio, mask = ParseOptionString("win,win", config.Platforms)
	if len(prio) != 2 || prio[0] != config.PlatformWindows || prio[1] != config.PlatformWindows || mask != config.PlatformWindows {
		t.Errorf("cross-group dup: prio=%v mask=0x%x", prio, mask)
	}
}

// TestOptionByID locks the exact-match lookup: the Galaxy layer resolves a
// language flag to its expression and an architecture flag to its code this way,
// and both fall back when nothing matches.
func TestOptionByID(t *testing.T) {
	o, ok := OptionByID(config.LangEN, config.Languages)
	if !ok || o.Code != "en" || o.Regexp != "en|eng|english|en[_-]US" {
		t.Errorf("OptionByID(LangEN) = %+v, %v", o, ok)
	}

	if _, ok := OptionByID(0, config.Languages); ok {
		t.Error("0 must match no entry")
	}
	// A composite flag equals no single entry: the lookup compares IDs for
	// equality.
	if _, ok := OptionByID(config.LangEN|config.LangDE, config.Languages); ok {
		t.Error("a composite flag must match no entry")
	}

	o, ok = OptionByID(config.ArchX64, config.GalaxyArchs)
	if !ok || o.Code != "64" {
		t.Errorf("OptionByID(ArchX64) = %+v, %v", o, ok)
	}
	if o, ok := OptionByID(config.ArchX86, config.GalaxyArchs); !ok || o.Code != "32" {
		t.Errorf("OptionByID(ArchX86) = %+v, %v", o, ok)
	}
}

func TestStrippedStringASCIIOnly(t *testing.T) {
	in := "Ab1 -_.[]{}() \t\né界"
	want := "Ab1 -_.[]{}() "
	if got := StrippedString(in); got != want {
		t.Errorf("StrippedString = %q, want %q", got, want)
	}
}

func TestEtaStringBoundaries(t *testing.T) {
	cases := []struct {
		sec  int64
		want string
	}{
		{0, "0s"},
		{1, "1s"},
		{59, "59s"},
		{60, "1m 00s"},
		{61, "1m 01s"},
		{3599, "59m 59s"},
		{3600, "1h 00m 00s"},
		{3601, "1h 00m 01s"},
		{86399, "23h 59m 59s"},
		{86400, "1d 00h 00m 00s"},
		{90061, "1d 01h 01m 01s"},
	}
	for _, c := range cases {
		if got := EtaString(c.sec); got != c.want {
			t.Errorf("EtaString(%d) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestSizeStringBoundaries(t *testing.T) {
	cases := []struct {
		bytes  uint64
		format uint32
		want   string
	}{
		{0, config.UnitFormatIEC, "0.00 B"},
		{1, config.UnitFormatIEC, "1.00 B"},
		{1023, config.UnitFormatIEC, "1023.00 B"},
		{1024, config.UnitFormatIEC, "1.00 KiB"},
		{1025, config.UnitFormatIEC, "1.00 KiB"},
		{1048575, config.UnitFormatIEC, "1024.00 KiB"}, // 1048575/1024 = 1023.999… rounds up
		{1048576, config.UnitFormatIEC, "1.00 MiB"},
		{1000, config.UnitFormatSI, "1.00 kB"},
		{999, config.UnitFormatSI, "999.00 B"},
		{1000000, config.UnitFormatSI, "1.00 MB"},
	}
	for _, c := range cases {
		if got := SizeString(c.bytes, c.format); got != c.want {
			t.Errorf("SizeString(%d,%d) = %q, want %q", c.bytes, c.format, got, c.want)
		}
	}
}

func TestRateStringBoundaries(t *testing.T) {
	cases := []struct {
		rate   float64
		format uint32
		want   string
	}{
		{0, config.UnitFormatIEC, "0.00KiB/s"},
		{999, config.UnitFormatIEC, "0.98KiB/s"},
		{1048575, config.UnitFormatIEC, "1024.00KiB/s"}, // 1048575/1024 = 1023.999… rounds up
		// Strictly greater-than boundary: equal to M uses the K branch.
		{1048576, config.UnitFormatIEC, "1024.00KiB/s"},
		{1048577, config.UnitFormatIEC, "1.00MiB/s"},
		{999, config.UnitFormatSI, "1.00kB/s"},
		{1000, config.UnitFormatSI, "1.00kB/s"},
		{999999, config.UnitFormatSI, "1000.00kB/s"},
		{1000000, config.UnitFormatSI, "1000.00kB/s"},
		{1000001, config.UnitFormatSI, "1.00MB/s"},
	}
	for _, c := range cases {
		if got := RateString(c.rate, c.format); got != c.want {
			t.Errorf("RateString(%v,%d) = %q, want %q", c.rate, c.format, got, c.want)
		}
	}
}

func TestJSONUintString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{float64(42), "42"},
		{float64(-1), ""},
		{float64(1.5), ""},
		{int64(7), "7"},
		{uint64(9), "9"},
		{true, ""},
	}
	for _, c := range cases {
		if got := JSONUintString(c.in); got != c.want {
			t.Errorf("JSONUintString(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadJSONFileErrorWrapsPath(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadJSONFile(filepath.Join(dir, "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if got := err.Error(); len(got) == 0 {
		t.Error("error message empty")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadJSONFile(bad)
	if err == nil {
		t.Fatal("expected parse error")
	}

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := ReadJSONFile(good)
	if err != nil {
		t.Fatalf("ReadJSONFile: %v", err)
	}
	obj, ok := v.(map[string]any)
	if !ok || obj["a"] != float64(1) {
		t.Errorf("ReadJSONFile value = %#v", v)
	}
}

// envGuard restores an environment variable after a test.
type envGuard struct {
	key   string
	value string
	ok    bool
}

func guardEnv(key string) *envGuard {
	v, ok := os.LookupEnv(key)
	return &envGuard{key: key, value: v, ok: ok}
}

func (g *envGuard) restore() {
	if g.ok {
		os.Setenv(g.key, g.value)
	} else {
		os.Unsetenv(g.key)
	}
}

func clearEnv(t *testing.T, key string) *envGuard {
	t.Helper()
	g := guardEnv(key)
	t.Cleanup(g.restore)
	os.Unsetenv(key)
	return g
}

func TestHomeDirMissing(t *testing.T) {
	clearEnv(t, "HOME")
	if _, err := HomeDir(); err == nil {
		t.Error("HomeDir must fail when HOME is unset")
	}
}

func TestHomeDirSet(t *testing.T) {
	clearEnv(t, "HOME")
	t.Setenv("HOME", "/tmp/testhome")
	if got, err := HomeDir(); err != nil || got != "/tmp/testhome" {
		t.Errorf("HomeDir = %q, %v", got, err)
	}
}

func TestConfigHomeSemantics(t *testing.T) {
	if usesStdlibRoots() {
		t.Skip("XDG semantics apply to the Unix branch only (see TestConfigHomeUsesPlatformRoots)")
	}
	g := clearEnv(t, "HOME")
	clearEnv(t, "XDG_CONFIG_HOME")
	t.Setenv("HOME", "/tmp/testhome")

	// XDG unset -> fallback.
	got, err := ConfigHome()
	if err != nil || got != filepath.Join("/tmp/testhome", ".config") {
		t.Errorf("ConfigHome unset = %q, %v", got, err)
	}
	_ = g

	// XDG set -> value.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/config")
	got, _ = ConfigHome()
	if got != "/tmp/config" {
		t.Errorf("ConfigHome set = %q", got)
	}

	// XDG set but empty -> empty string.
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = ConfigHome()
	if err != nil || got != "" {
		t.Errorf("ConfigHome empty = %q, %v (want empty, nil)", got, err)
	}
}

func TestCacheHomeSemantics(t *testing.T) {
	if usesStdlibRoots() {
		t.Skip("XDG semantics apply to the Unix branch only (see TestCacheHomeUsesPlatformRoots)")
	}
	clearEnv(t, "HOME")
	clearEnv(t, "XDG_CACHE_HOME")
	t.Setenv("HOME", "/tmp/testhome")

	got, err := CacheHome()
	if err != nil || got != filepath.Join("/tmp/testhome", ".cache") {
		t.Errorf("CacheHome unset = %q, %v", got, err)
	}

	t.Setenv("XDG_CACHE_HOME", "/tmp/cache")
	if got, _ := CacheHome(); got != "/tmp/cache" {
		t.Errorf("CacheHome set = %q", got)
	}

	t.Setenv("XDG_CACHE_HOME", "")
	if got, err := CacheHome(); err != nil || got != "" {
		t.Errorf("CacheHome empty = %q, %v", got, err)
	}
}

// TestConfigHomeUsesPlatformRoots locks the platform split: on Windows
// and macOS the configuration root comes from the standard library, which is
// what lets the CLI start when HOME is not set.
func TestConfigHomeUsesPlatformRoots(t *testing.T) {
	if !usesStdlibRoots() {
		t.Skip("XDG branch is covered by TestConfigHomeSemantics")
	}
	clearEnv(t, "HOME") // the point of the platform split: no HOME required
	clearEnv(t, "XDG_CONFIG_HOME")

	want, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}
	got, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome without HOME: %v", err)
	}
	if got != want {
		t.Errorf("ConfigHome = %q, want the platform root %q", got, want)
	}
	t.Logf("config root on %s: %q", runtime.GOOS, got)
}

// TestCacheHomeUsesPlatformRoots is the cache counterpart; on Windows the two
// roots must stay distinct (%AppData% vs %LocalAppData%).
func TestCacheHomeUsesPlatformRoots(t *testing.T) {
	if !usesStdlibRoots() {
		t.Skip("XDG branch is covered by TestCacheHomeSemantics")
	}
	clearEnv(t, "HOME")
	clearEnv(t, "XDG_CACHE_HOME")

	want, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("os.UserCacheDir: %v", err)
	}
	got, err := CacheHome()
	if err != nil {
		t.Fatalf("CacheHome without HOME: %v", err)
	}
	if got != want {
		t.Errorf("CacheHome = %q, want the platform root %q", got, want)
	}
	t.Logf("cache root on %s: %q", runtime.GOOS, got)

	if runtime.GOOS == "windows" {
		cfgRoot, err := ConfigHome()
		if err != nil {
			t.Fatalf("ConfigHome: %v", err)
		}
		if cfgRoot == got {
			t.Errorf("config and cache roots must differ on Windows (got %q twice)", got)
		}
	}
}

func TestReplaceOnce(t *testing.T) {
	out, ok := ReplaceOnce("abcabc", "bc", "X")
	if !ok || out != "aXabc" {
		t.Errorf("ReplaceOnce = %q, %v", out, ok)
	}
	out, ok = ReplaceOnce("abc", "zz", "X")
	if ok || out != "abc" {
		t.Errorf("ReplaceOnce miss = %q, %v", out, ok)
	}
	out, ok = ReplaceOnce("abc", "", "X")
	if ok || out != "abc" {
		t.Errorf("ReplaceOnce empty old = %q, %v (guard)", out, ok)
	}
}

func TestReplaceAll(t *testing.T) {
	out, ok := ReplaceAll("abcabc", "bc", "X")
	if !ok || out != "aXaX" {
		t.Errorf("ReplaceAll = %q, %v", out, ok)
	}
	// Replaced segments are not rescanned, so this input terminates.
	out, ok = ReplaceAll("a", "a", "aa")
	if !ok || out != "aa" {
		t.Errorf("ReplaceAll rescan case = %q, %v", out, ok)
	}
	if out, ok := ReplaceAll("abc", "zz", "X"); ok || out != "abc" {
		t.Errorf("ReplaceAll miss = %q, %v", out, ok)
	}
	if out, ok := ReplaceAll("abc", "", "X"); ok || out != "abc" {
		t.Errorf("ReplaceAll empty old = %q, %v", out, ok)
	}
}
