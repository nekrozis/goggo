package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/util"
)

// installManifest is a manifest shaped like the one a build returns, with a
// decoy productId: only baseProductId may feed %product_id%.
func installManifest() map[string]any {
	return map[string]any{
		"baseProductId":    "1495134320",
		"productId":        "9999999999",
		"installDirectory": "The Witcher 3: Wild Hunt - GOTY",
	}
}

// TestResolveInstallSubdir locks the six names and, above all, that matching is
// whole-string: upstream looks the value up in a map rather than substituting
// placeholders inside a longer path (downloader.cpp:6686-6698).
func TestResolveInstallSubdir(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{name: "install_dir", template: "%install_dir%", want: "The Witcher 3: Wild Hunt - GOTY"},
		{name: "product_id comes from baseProductId", template: "%product_id%", want: "1495134320"},
		{
			name:     "install_dir_stripped",
			template: "%install_dir_stripped%",
			want:     util.StrippedString("The Witcher 3: Wild Hunt - GOTY"),
		},
		{name: "plain path is untouched", template: "games", want: "games"},
		{name: "empty stays empty", template: "", want: ""},
		{
			// The whole-string rule: the prefix is not expanded.
			name:     "no substitution inside a longer path",
			template: "%install_dir%/data",
			want:     "%install_dir%/data",
		},
		{name: "unknown name is untouched", template: "%gamename_stripped%", want: "%gamename_stripped%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveInstallSubdir(c.template, installManifest())
			if err != nil {
				t.Fatalf("ResolveInstallSubdir(%q): %v", c.template, err)
			}
			if got != c.want {
				t.Errorf("ResolveInstallSubdir(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}

// TestResolveInstallSubdirUnsupported locks the three templates whose value
// needs product information this build does not fetch yet.
func TestResolveInstallSubdirUnsupported(t *testing.T) {
	for _, template := range []string{"%gamename%", "%title%", "%title_stripped%"} {
		t.Run(template, func(t *testing.T) {
			got, err := ResolveInstallSubdir(template, installManifest())
			if !errors.Is(err, ErrUnsupportedTemplate) {
				t.Fatalf("err = %v, want ErrUnsupportedTemplate", err)
			}
			if !strings.Contains(err.Error(), template) {
				t.Errorf("err = %q, want it to name %s", err.Error(), template)
			}
			if got != "" {
				t.Errorf("value = %q, want nothing", got)
			}
		})
	}
}

// TestResolveInstallSubdirManifestShape locks the document contract: a missing
// or null member is empty, a structured one is an error rather than a crash.
func TestResolveInstallSubdirManifestShape(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]any
	}{
		{name: "nil manifest", manifest: nil},
		{name: "empty manifest", manifest: map[string]any{}},
		{
			name:     "null members",
			manifest: map[string]any{"installDirectory": nil, "baseProductId": nil},
		},
		{name: "unrelated manifest", manifest: map[string]any{"depots": []any{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := ResolveInstallSubdir("%install_dir%", c.manifest); err != nil || got != "" {
				t.Errorf("install_dir = %q, %v; want empty and no error", got, err)
			}
			if got, err := ResolveInstallSubdir("%product_id%", c.manifest); err != nil || got != "" {
				t.Errorf("product_id = %q, %v; want empty and no error", got, err)
			}
		})
	}

	for _, key := range []string{"installDirectory", "baseProductId"} {
		t.Run("structured "+key, func(t *testing.T) {
			template := "%install_dir%"
			if key == "baseProductId" {
				template = "%product_id%"
			}
			_, err := ResolveInstallSubdir(template, map[string]any{key: map[string]any{}})
			if err == nil {
				t.Fatal("a structured member must be an error")
			}
			if errors.Is(err, ErrUnsupportedTemplate) {
				t.Error("a malformed document is not an unsupported template")
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("err = %q, want it to name %s", err.Error(), key)
			}
		})
	}
}
