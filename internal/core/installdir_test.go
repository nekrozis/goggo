package core

import (
	"strings"
	"testing"
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

const (
	installProductSlug  = "the_witcher_3_wild_hunt"
	installProductTitle = "The Witcher 3: Wild Hunt"
)

// TestInstallSubdirTemplateTable locks the exported list against the resolver
// and the predicate, which is what makes the list a source of truth rather than
// a table of its own: every name it carries resolves to the documented value
// (a name the resolver does not implement would come back as its own literal
// text), the predicate answers true exactly for the names whose value comes
// from the product document, and the two agree about what a missing document
// does. A name outside the list is rejected by both.
func TestInstallSubdirTemplateTable(t *testing.T) {
	documented := []struct {
		template  string
		want      string
		needsInfo bool
	}{
		{"%install_dir%", "The Witcher 3: Wild Hunt - GOTY", false},
		{"%product_id%", "1495134320", false},
		{"%install_dir_stripped%", "The Witcher 3 Wild Hunt - GOTY", false},
		{"%gamename%", "the_witcher_3_wild_hunt", true},
		{"%title%", "The Witcher 3: Wild Hunt", true},
		{"%title_stripped%", "The Witcher 3 Wild Hunt", true},
	}
	values := make(map[string]string, len(documented))
	for _, d := range documented {
		values[d.template] = d.want
	}

	seen := make(map[string]bool, len(InstallSubdirTemplates))
	for _, template := range InstallSubdirTemplates {
		if seen[template] {
			t.Errorf("%s is listed twice", template)
		}
		seen[template] = true
		want, ok := values[template]
		if !ok {
			t.Errorf("%s is exported but has no documented resolution", template)
			continue
		}
		got, err := ResolveInstallSubdir(template, installManifest(), installProductSlug, installProductTitle)
		if err != nil {
			t.Errorf("ResolveInstallSubdir(%q): %v", template, err)
			continue
		}
		if got != want {
			t.Errorf("ResolveInstallSubdir(%q) = %q, want %q", template, got, want)
		}
	}

	for _, d := range documented {
		if !IsInstallSubdirTemplate(d.template) {
			t.Errorf("IsInstallSubdirTemplate(%q) = false, want the exported list to carry every documented template", d.template)
		}
	}

	for _, d := range documented {
		if got := InstallSubdirNeedsProductInfo(d.template); got != d.needsInfo {
			t.Errorf("InstallSubdirNeedsProductInfo(%q) = %v, want %v", d.template, got, d.needsInfo)
		}
		withProduct, err := ResolveInstallSubdir(d.template, installManifest(), installProductSlug, installProductTitle)
		if err != nil {
			t.Fatalf("ResolveInstallSubdir(%q): %v", d.template, err)
		}
		withoutProduct, err := ResolveInstallSubdir(d.template, installManifest(), "", "")
		if err != nil {
			t.Fatalf("ResolveInstallSubdir(%q): %v", d.template, err)
		}
		if d.needsInfo {
			if withoutProduct != d.template {
				t.Errorf("ResolveInstallSubdir(%q) without a product document = %q, want the literal name", d.template, withoutProduct)
			}
			continue
		}
		if withoutProduct != withProduct {
			t.Errorf("ResolveInstallSubdir(%q) without a product document = %q, want the same %q as with one",
				d.template, withoutProduct, withProduct)
		}
	}

	for _, name := range []string{"", "games", "%foo%", "%install_dir%/data", "%install_dir%x", "%GAMENAME%"} {
		if IsInstallSubdirTemplate(name) {
			t.Errorf("IsInstallSubdirTemplate(%q) = true, want false", name)
		}
	}
}

// TestResolveInstallSubdir locks the six names, the values each one reads and,
// above all, that matching is whole-string: the value is looked up as a whole
// name rather than substituting placeholders inside a longer path.
func TestResolveInstallSubdir(t *testing.T) {
	cases := []struct {
		name     string
		template string
		slug     string
		title    string
		want     string
	}{
		{name: "install_dir", template: "%install_dir%", want: "The Witcher 3: Wild Hunt - GOTY"},
		{name: "product_id comes from baseProductId", template: "%product_id%", want: "1495134320"},
		{
			name:     "install_dir_stripped",
			template: "%install_dir_stripped%",
			want:     "The Witcher 3 Wild Hunt - GOTY",
		},
		{name: "gamename is the slug", template: "%gamename%", slug: installProductSlug, title: installProductTitle, want: "the_witcher_3_wild_hunt"},
		{name: "title", template: "%title%", slug: installProductSlug, title: installProductTitle, want: "The Witcher 3: Wild Hunt"},
		{
			name:     "title_stripped comes from title",
			template: "%title_stripped%",
			slug:     installProductSlug,
			title:    installProductTitle,
			want:     "The Witcher 3 Wild Hunt",
		},
		{name: "plain path is untouched", template: "games", want: "games"},
		{name: "empty stays empty", template: "", want: ""},
		{
			name:     "no substitution inside a longer path",
			template: "%install_dir%/data",
			want:     "%install_dir%/data",
		},
		{
			name:     "an information template inside a longer path is not expanded either",
			template: "%gamename%/data",
			slug:     installProductSlug,
			title:    installProductTitle,
			want:     "%gamename%/data",
		},
		{name: "unknown name is untouched", template: "%gamename_stripped%", want: "%gamename_stripped%"},
		{name: "dashes are not a template", template: "-gamename-", want: "-gamename-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveInstallSubdir(c.template, installManifest(), c.slug, c.title)
			if err != nil {
				t.Fatalf("ResolveInstallSubdir(%q): %v", c.template, err)
			}
			if got != c.want {
				t.Errorf("ResolveInstallSubdir(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}

// TestResolveInstallSubdirEmptyProductInfo locks the missing-value rule: a
// template whose value is missing is registered with nothing, so the lookup
// misses and the NAME ITSELF is the result.
func TestResolveInstallSubdirEmptyProductInfo(t *testing.T) {
	cases := []struct {
		name          string
		slug          string
		title         string
		gamename      string
		wantTitle     string
		titleStripped string
	}{
		{
			name:          "no product info at all",
			gamename:      "%gamename%",
			wantTitle:     "%title%",
			titleStripped: "%title_stripped%",
		},
		{
			name:          "empty slug",
			slug:          "",
			title:         "A Title",
			gamename:      "%gamename%",
			wantTitle:     "A Title",
			titleStripped: "A Title",
		},
		{
			name:          "empty title",
			slug:          "some_game",
			title:         "",
			gamename:      "some_game",
			wantTitle:     "%title%",
			titleStripped: "%title_stripped%",
		},
		{
			name:          "both empty",
			slug:          "",
			title:         "",
			gamename:      "%gamename%",
			wantTitle:     "%title%",
			titleStripped: "%title_stripped%",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for template, want := range map[string]string{
				"%gamename%":       c.gamename,
				"%title%":          c.wantTitle,
				"%title_stripped%": c.titleStripped,
			} {
				got, err := ResolveInstallSubdir(template, installManifest(), c.slug, c.title)
				if err != nil {
					t.Fatalf("ResolveInstallSubdir(%q): %v", template, err)
				}
				if got != want {
					t.Errorf("ResolveInstallSubdir(%q) = %q, want %q", template, got, want)
				}
			}
		})
	}

	if got, err := ResolveInstallSubdir("%title%/setup", installManifest(), "some_game", ""); err != nil {
		t.Fatalf("ResolveInstallSubdir: %v", err)
	} else if got != "%title%/setup" {
		t.Errorf("value = %q, want %q", got, "%title%/setup")
	}

	if got, err := ResolveInstallSubdir("%product_id%", installManifest(), "", ""); err != nil {
		t.Errorf("ResolveInstallSubdir(%s): %v", "%product_id%", err)
	} else if got != "1495134320" {
		t.Errorf("value = %q, want the manifest's base product id", got)
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
			if got, err := ResolveInstallSubdir("%install_dir%", c.manifest, "", ""); err != nil || got != "" {
				t.Errorf("install_dir = %q, %v; want empty and no error", got, err)
			}
			if got, err := ResolveInstallSubdir("%product_id%", c.manifest, "", ""); err != nil || got != "" {
				t.Errorf("product_id = %q, %v; want empty and no error", got, err)
			}
			if got, err := ResolveInstallSubdir("%install_dir_stripped%", c.manifest, "", ""); err != nil || got != "" {
				t.Errorf("install_dir_stripped = %q, %v; want empty and no error", got, err)
			}
		})
	}

	for _, key := range []string{"installDirectory", "baseProductId"} {
		t.Run("structured "+key, func(t *testing.T) {
			template := "%install_dir%"
			if key == "baseProductId" {
				template = "%product_id%"
			}
			_, err := ResolveInstallSubdir(template, map[string]any{key: map[string]any{}}, "", "")
			if err == nil {
				t.Fatal("a structured member must be an error")
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("err = %q, want it to name %s", err.Error(), key)
			}
		})
	}

	if _, err := ResolveInstallSubdir("%install_dir%", map[string]any{"baseProductId": map[string]any{}}, "", ""); err == nil {
		t.Error("a malformed baseProductId must be an error whatever the template is")
	}
}

// TestResolveInstallSubdirWithoutProduct locks the pairing of the predicate and
// the resolver: the three information names answer with their literal text when
// no document was fetched.
func TestResolveInstallSubdirWithoutProduct(t *testing.T) {
	for _, template := range InstallSubdirTemplates {
		if !InstallSubdirNeedsProductInfo(template) {
			continue
		}
		got, err := ResolveInstallSubdir(template, installManifest(), "", "")
		if err != nil {
			t.Fatalf("ResolveInstallSubdir(%q): %v", template, err)
		}
		if got != template {
			t.Errorf("ResolveInstallSubdir(%q) = %q, want the literal name", template, got)
		}
	}
}

// TestResolveInstallSubdirErrorsAreNotSilent locks the failure modes apart.
func TestResolveInstallSubdirErrorsAreNotSilent(t *testing.T) {
	if _, err := ResolveInstallSubdir("%install_dir%", map[string]any{"installDirectory": []any{}}, "", ""); err == nil {
		t.Fatal("want an error for a structured installDirectory")
	}
}
