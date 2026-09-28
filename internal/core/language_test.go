package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/galaxy"
)

func languageCandidate(langs ...string) depotCandidate {
	return depotCandidate{manifestHash: "h", languages: langs}
}

// TestOfferedLanguages locks the source of truth: the distinct language tokens
// the base depots declare, deduplicated by their comparison form and sorted. A
// wildcard declares no language and contributes nothing.
func TestOfferedLanguages(t *testing.T) {
	got := offeredLanguages([]depotCandidate{
		languageCandidate("en-US", "de-DE"),
		languageCandidate("EN_us"), // the same token as en-US
		languageCandidate("*"),
		languageCandidate(),
	})
	want := []string{"de-DE", "en-US"}
	if len(got) != len(want) {
		t.Fatalf("offered = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("offered[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(offeredLanguages([]depotCandidate{languageCandidate("*")})) != 0 {
		t.Error("a wildcard-only build must offer no specific language")
	}
}

// TestResolveLanguageTokens locks the whole language rule, one row per case of
// the decided matrix: normalized exact match, family match, the union of every
// match, and the three ways a request can fail to apply.
func TestResolveLanguageTokens(t *testing.T) {
	enUS := languageCandidate("en-US")
	bothEnglish := []depotCandidate{languageCandidate("en-US"), languageCandidate("en-GB")}
	bothChinese := []depotCandidate{languageCandidate("zh-Hans", "zh-Hant")}

	cases := []struct {
		name       string
		candidates []depotCandidate
		requested  string
		want       []string
		wantErr    bool
	}{
		{"exact token selects itself", bothEnglish, "en-US", []string{"en-US"}, false},
		{"family selects the only member", []depotCandidate{enUS}, "en", []string{"en-US"}, false},
		{"family selects every member", bothEnglish, "en", []string{"en-GB", "en-US"}, false},
		{"normalized exact match", bothChinese, "ZH_hant", []string{"zh-Hant"}, false},
		{"family union", bothChinese, "zh", []string{"zh-Hans", "zh-Hant"}, false},
		{"a specific token narrows the union", bothChinese, "zh-Hant", []string{"zh-Hant"}, false},
		{"nothing matches", bothEnglish, "klingon", nil, true},
		// jp is a table code, not a build token: ja-JP cannot be derived from
		// it, so v1 refuses it and lists what the build really declares.
		{"a legacy table code is not an alias", []depotCandidate{languageCandidate("ja-JP")}, "jp", nil, true},
		// A build that declares no language at all is language-agnostic: the
		// wildcard is what selects its content.
		{"language-agnostic build", []depotCandidate{languageCandidate("*")}, "en", []string{"*"}, false},
		// A wildcard depot alongside a specific one does not widen the request.
		{"wildcard beside a specific depot", []depotCandidate{languageCandidate("*", "en-US")}, "en", []string{"en-US"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveLanguageTokens(c.candidates, c.requested, "1207658991")
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveLanguageTokens(%q) succeeded, want an error", c.requested)
				}
				if _, ok := errors.AsType[*UsageError](err); !ok {
					t.Errorf("err = %v, want a usage-class refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveLanguageTokens(%q): %v", c.requested, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("tokens = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("tokens[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestResolveLanguageTokensErrorNamesWhatTheBuildOffers locks the message an
// unknown request produces: the request itself, what the build does declare, and
// the command that lists it.
func TestResolveLanguageTokensErrorNamesWhatTheBuildOffers(t *testing.T) {
	_, err := resolveLanguageTokens([]depotCandidate{languageCandidate("zh-Hant")}, "klingon", "42")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"klingon", "zh-Hant", "goggo install options 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// TestBuildPlanSelectsEveryMatchingLanguage locks the union: a request naming a
// language family installs every variant the build declares, and says so once.
func TestBuildPlanSelectsEveryMatchingLanguage(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	enUSHash := "1a00000000000000000000000000000000000001"
	enGBHash := "1a00000000000000000000000000000000000002"
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"Both English","version":2,`+
			`"products":[{"name":"Both English"}],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+enUSHash+`"},`+
			`{"productId":"`+planProductID+`","languages":["en-GB"],"osBitness":["64"],"manifest":"`+enGBHash+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(enUSHash),
		`{"depot":{"items":[{"path":"game/us.bin","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":10}]}]}}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(enGBHash),
		`{"depot":{"items":[{"path":"game/gb.bin","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":10,"size":10}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	paths := map[string]bool{}
	for _, task := range res.Plan.Tasks {
		paths[task.Item.Path] = true
	}
	for _, want := range []string{"game/us.bin", "game/gb.bin"} {
		if !paths[want] {
			t.Errorf("plan is missing %s: every language the request covers must be installed", want)
		}
	}

	notice := ""
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "matches several languages") {
			notice = m.Text
		}
	}
	if notice == "" {
		t.Fatalf("no notice for a request covering two languages: %+v", res.Messages)
	}
	for _, want := range []string{"en-US", "en-GB"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice = %q, want it to name %s", notice, want)
		}
	}
}

// TestBuildPlanInstallsALanguageAgnosticGame locks the wildcard case: a build
// whose base depots declare no language at all must still install under the
// default request, and must not claim to have resolved several languages.
func TestBuildPlanInstallsALanguageAgnosticGame(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	hash := "1b00000000000000000000000000000000000001"
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"Agnostic","version":2,`+
			`"products":[{"name":"Agnostic"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["*"],"osBitness":["64"],"manifest":"`+hash+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(hash),
		`{"depot":{"items":[{"path":"game/any.bin","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":10}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(res.Plan.Tasks) != 1 || res.Plan.Tasks[0].Item.Path != "game/any.bin" {
		t.Fatalf("tasks = %+v, want the wildcard depot's file", res.Plan.Tasks)
	}
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "matches several languages") {
			t.Errorf("one wildcard resolution must not print a notice: %q", m.Text)
		}
	}
}
