package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	jsonv2 "encoding/json/v2"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/galaxy"
)

const (
	fixtureWormsBuildHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixtureDepotEN64      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fixtureDepotEN32      = "cccccccccccccccccccccccccccccccccccccccc"
	fixtureDepotFR64      = "dddddddddddddddddddddddddddddddddddddddd"
	fixtureDepotSupport   = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func newInstallOptionsFixture(t *testing.T) core.Dependencies {
	t.Helper()
	isolateRoots(t)

	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	seed, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	const token = `{"access_token":"at","refresh_token":"rt","expires_in":3600,"user_id":"u1"}`
	var fields map[string]any
	if err := jsonv2.Unmarshal([]byte(token), &fields); err != nil {
		t.Fatal(err)
	}
	seed.StoreLoginResponse(fields)
	if err := seed.Save(); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	w1Path := galaxy.HashToGalaxyPath(fixtureWormsBuildHash)
	dEN64Path := galaxy.HashToGalaxyPath(fixtureDepotEN64)
	dEN32Path := galaxy.HashToGalaxyPath(fixtureDepotEN32)
	dFR64Path := galaxy.HashToGalaxyPath(fixtureDepotFR64)
	dSupportPath := galaxy.HashToGalaxyPath(fixtureDepotSupport)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/products/1207658991/os/windows/builds"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"items":[{"build_id":"51234","generation":2,"link":"https://cdn.gog.com/content-system/v2/meta/%s"}]}`, w1Path)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+w1Path):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"baseProductId":"1207658991","version":2,"installDirectory":"Worms United",`+
				`"products":[{"name":"Worms United"}],`+
				`"depots":[`+
				`{"productId":"1207658991","languages":["en-US"],"osBitness":["64"],"size":1240000000,"manifest":"%s"},`+
				`{"productId":"1207658991","languages":["en-US"],"osBitness":["32"],"size":1240000000,"manifest":"%s"},`+
				`{"productId":"1207658991","languages":["fr-FR"],"osBitness":["64"],"size":1250000000,"manifest":"%s"},`+
				`{"productId":"1207658991","isGogDepot":true,"languages":["*"],"size":3545,"manifest":"%s"}]}`,
				fixtureDepotEN64, fixtureDepotEN32, fixtureDepotFR64, fixtureDepotSupport)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+dEN64Path):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"depot":{"items":[{"path":"game/worms.exe","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":100,"size":100}]},`+
				`{"path":"game/data.bin","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":200,"size":200}]}]}}`)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+dEN32Path):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"depot":{"items":[{"path":"game/worms.exe","chunks":[{"compressedMd5":"c3","md5":"u3","compressedSize":90,"size":90}]},`+
				`{"path":"game/data.bin","chunks":[{"compressedMd5":"c4","md5":"u4","compressedSize":200,"size":200}]}]}}`)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+dFR64Path):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"depot":{"items":[{"path":"game/worms.exe","chunks":[{"compressedMd5":"c5","md5":"u5","compressedSize":100,"size":100}]},`+
				`{"path":"game/data_fr.bin","chunks":[{"compressedMd5":"c6","md5":"u6","compressedSize":210,"size":210}]}]}}`)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+dSupportPath):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"depot":{"items":[{"path":"goggame-1207658991.info","chunks":[{"compressedMd5":"c7","md5":"u7","compressedSize":50,"size":50}]}]}}`)
		case r.URL.Path == "/www/account":
			fmt.Fprint(w, "account")
		case r.URL.Path == "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["1207658991"]}`)
		case r.URL.Path == "/www/account/getFilteredProducts":
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[{"id":"1207658991","slug":"worms_united"}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	return core.Dependencies{HTTPTransport: &hostRedirectTransport{target: target}}
}

func TestInstallOptionsCLIText(t *testing.T) {
	deps := newInstallOptionsFixture(t)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"install", "options", "1207658991"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("install options exited %d, stderr: %s", code, errOut.String())
	}
	res := out.String()
	for _, want := range []string{
		`Available installation options for "Worms United" (Build: 51234):`,
		"PLATFORM",
		"ARCH",
		"LANGUAGE",
		"SIZE (EST)",
		"FILES",
		"windows",
		"x64",
		"en-US",
		"1.15 GiB",
		"x86",
		"fr-FR",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("stdout missing %q, got:\n%s", want, res)
		}
	}
	if strings.Contains(res, "goggame-1207658991.info") || strings.Contains(res, "3.46 KiB") {
		t.Errorf("support metadata depot must be excluded from options: %s", res)
	}
}

func TestInstallOptionsCLIJSON(t *testing.T) {
	deps := newInstallOptionsFixture(t)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"install", "options", "1207658991", "--json"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("install options --json exited %d, stderr: %s", code, errOut.String())
	}

	var res core.InstallOptionsResult
	if err := jsonv2.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("unmarshal install options --json: %v\nOutput: %s", err, out.String())
	}
	if res.GameTitle != "Worms United" || res.BuildID != "51234" {
		t.Errorf("result title/build mismatch: %+v", res)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("entries count = %d, want 3", len(res.Entries))
	}
}

func TestInstallZeroMatchCLI(t *testing.T) {
	deps := newInstallOptionsFixture(t)
	installDir := t.TempDir()

	var out, errOut strings.Builder
	code := runWithDeps([]string{
		"install",
		"--platform", "windows",
		"--language", "zh-Hans",
		"--arch", "x64",
		"--directory", installDir,
		"1207658991",
	}, strings.NewReader(""), &out, &errOut, deps)

	if code != 1 {
		t.Fatalf("install 0-match exit = %d, want 1. stdout: %s, stderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "no compatible content found matching platform=windows, language=zh-Hans, arch=x64") {
		t.Errorf("stderr missing expected error message, got:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "Use 'goggo install options 1207658991' to view available combinations") {
		t.Errorf("stderr missing guidance hint, got:\n%s", errOut.String())
	}

	// Case H: Verify filesystem isolation — install directory remains completely unmodified.
	entries, err := os.ReadDir(installDir)
	if err != nil {
		t.Fatalf("ReadDir installDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("install directory must not have any created files, found %d entries", len(entries))
	}
}
