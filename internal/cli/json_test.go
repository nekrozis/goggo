package cli

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// setupJSONTestServer creates a mock HTTP test server serving endpoints needed
// across the unified --json test suite.
func setupJSONTestServer(t *testing.T) (*httptest.Server, core.Dependencies) {
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
	seed.StoreLoginResponse(map[string]any{
		"access_token": "json_at", "refresh_token": "json_rt", "expires_in": 3600, "user_id": "u1",
	})
	if err := seed.Save(); err != nil {
		t.Fatalf("seed.Save: %v", err)
	}

	buildHash := "1111111111111111111111111111111111111111"
	depotHash := "2222222222222222222222222222222222222222"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/www/account" || r.URL.Path == "/account":
			fmt.Fprint(w, "account")
		case strings.Contains(r.URL.Path, "/user/data/games"):
			fmt.Fprint(w, `{"owned":[1207658991]}`)
		case strings.Contains(r.URL.Path, "/account/getFilteredProducts") || strings.Contains(r.URL.Path, "/account/wishlist/search"):
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[{"id":1207658991,"title":"Worms United","slug":"worms_united","updates":0,"isNew":false,"worksOn":{"Windows":true}}]}`)
		case strings.Contains(r.URL.Path, "/tags"):
			fmt.Fprint(w, `{"tags":[{"id":"1","name":"Action"},{"id":"2","name":"Strategy"}]}`)
		case strings.Contains(r.URL.Path, "/products/1207658991/os/windows/builds"):
			fmt.Fprintf(w, `{"items":[{"build_id":"b1","version_name":"1.0","date_published":"2024-01-01","generation":2,"link":"https://cdn.gog.com/content-system/v2/meta/%s"}]}`,
				galaxy.HashToGalaxyPath(buildHash))
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+galaxy.HashToGalaxyPath(buildHash)):
			fmt.Fprintf(w, `{"baseProductId":"1207658991","installDirectory":"Worms","version":2,"depots":[{"productId":"1207658991","languages":["en-US"],"osBitness":["64"],"manifest":"%s"}]}`,
				depotHash)
		case strings.Contains(r.URL.Path, "/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHash)):
			fmt.Fprint(w, `{"depot":{"items":[{"path":"worms.exe","md5":"abc","total_size":100}]}}`)
		case strings.Contains(r.URL.Path, "/products/1207658991/secure_link"):
			fmt.Fprint(w, `{"urls":[{"endpoint_name":"cdn1","url_format":"https://cdn1.example.com/{path}"},{"endpoint_name":"cdn2","url_format":"https://cdn2.example.com/{path}"}]}`)
		case strings.Contains(r.URL.Path, "/account/gameDetails/1207658991.json"):
			fmt.Fprint(w, `{"title":"Worms United","icon":"//images.example.com/icon.png","downloads":[["English",{"windows":[{"id":"installer1","name":"Setup","path":"/setup.exe","size":"10 MB","version":"1.0"}]}]]}`)
		case strings.Contains(r.URL.Path, "/products/1207658991"):
			fmt.Fprint(w, fixtureGameJSON)
		default:
			http.NotFound(w, r)
		}
	}))

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	deps := core.Dependencies{HTTPTransport: &hostRedirectTransport{target: u}}
	return server, deps
}

// TestJSONOutputUnifiedAndDecodable validates that all 9 --json commands output
// pure, parseable JSON via encoding/json/v2.
func TestJSONOutputUnifiedAndDecodable(t *testing.T) {
	server, deps := setupJSONTestServer(t)
	defer server.Close()

	commands := []struct {
		name string
		args []string
	}{
		{"list games", []string{"list", "games", "--json"}},
		{"list tags", []string{"list", "tags", "--json"}},
		{"list wishlist", []string{"list", "wishlist", "--json"}},
		{"game", []string{"game", "1207658991", "--json"}},
		{"galaxy builds", []string{"galaxy", "builds", "1207658991", "--json"}},
		{"galaxy manifest", []string{"galaxy", "manifest", "1207658991", "--json"}},
		{"galaxy cdns", []string{"galaxy", "cdns", "1207658991", "--json"}},
		{"install options", []string{"install", "options", "1207658991", "--json"}},
		{"backup list", []string{"backup", "list", "1207658991", "--json"}},
	}

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithDeps(tc.args, strings.NewReader(""), &stdout, &stderr, deps)
			if code != 0 {
				t.Fatalf("command %v failed (exit %d), stderr: %s", tc.args, code, stderr.String())
			}

			outBytes := stdout.Bytes()
			if len(bytes.TrimSpace(outBytes)) == 0 {
				t.Fatalf("command %v produced empty stdout", tc.args)
			}

			var payload any
			if err := jsonv2.Unmarshal(outBytes, &payload); err != nil {
				t.Fatalf("command %v stdout is not valid JSON: %v\nOutput:\n%s", tc.args, err, stdout.String())
			}
		})
	}
}

// TestJSONModeSeparationWithVerbose validates that when --verbose is supplied with
// --json, stdout remains 100% pure decodable JSON and all diagnostics go to stderr.
func TestJSONModeSeparationWithVerbose(t *testing.T) {
	server, deps := setupJSONTestServer(t)
	defer server.Close()

	commands := [][]string{
		{"list", "games", "--json", "-v"},
		{"list", "tags", "--json", "-v"},
		{"list", "wishlist", "--json", "-v"},
		{"game", "1207658991", "--json", "-v"},
		{"galaxy", "builds", "1207658991", "--json", "-v"},
		{"galaxy", "manifest", "1207658991", "--json", "-v"},
		{"galaxy", "cdns", "1207658991", "--json", "-v"},
		{"install", "options", "1207658991", "--json", "-v"},
		{"backup", "list", "1207658991", "--json", "-v"},
	}

	for _, args := range commands {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithDeps(args, strings.NewReader(""), &stdout, &stderr, deps)
			if code != 0 {
				t.Fatalf("command %v failed (exit %d), stderr: %s", args, code, stderr.String())
			}

			var payload any
			if err := jsonv2.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatalf("command %v with -v corrupted stdout JSON: %v\nStdout:\n%s\nStderr:\n%s",
					args, err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "verbose:") {
				t.Errorf("command %v with -v expected verbose diagnostic on stderr, got: %q", args, stderr.String())
			}
			if strings.Contains(stdout.String(), "verbose:") {
				t.Errorf("command %v with -v leaked verbose diagnostic into stdout:\n%s", args, stdout.String())
			}
		})
	}
}

// TestJSONNoticeOnlySuccessPayload validates that when a command encounters a
// Notice-only success (such as Linux fallback notice), stdout still outputs a
// strictly defined JSON payload and the notice goes to stderr.
func TestJSONNoticeOnlySuccessPayload(t *testing.T) {
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
	seed.StoreLoginResponse(map[string]any{
		"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": "u1",
	})
	if err := seed.Save(); err != nil {
		t.Fatalf("seed.Save: %v", err)
	}

	// Server returning empty products so resolving "nomatch" yields a non-error Notice.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/www/account" || r.URL.Path == "/account":
			fmt.Fprint(w, "account")
		case strings.Contains(r.URL.Path, "/user/data/games"):
			fmt.Fprint(w, `{"owned":[]}`)
		case strings.Contains(r.URL.Path, "/account/getFilteredProducts"):
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	deps := core.Dependencies{HTTPTransport: &hostRedirectTransport{target: u}}

	// 1. galaxy builds Notice-only success -> []
	var stdoutBuilds, stderrBuilds bytes.Buffer
	code := runWithDeps([]string{"galaxy", "builds", "nomatch", "--json"}, strings.NewReader(""), &stdoutBuilds, &stderrBuilds, deps)
	if code != 0 {
		t.Fatalf("galaxy builds exit = %d, want 0 (stderr: %s)", code, stderrBuilds.String())
	}
	var buildsPayload []any
	if err := jsonv2.Unmarshal(stdoutBuilds.Bytes(), &buildsPayload); err != nil {
		t.Fatalf("galaxy builds stdout is not a valid JSON array: %v\nStdout: %s", err, stdoutBuilds.String())
	}
	if len(buildsPayload) != 0 {
		t.Errorf("galaxy builds notice-only payload = %v, want []", buildsPayload)
	}
	if stderrBuilds.Len() == 0 {
		t.Error("galaxy builds notice-only expected notice in stderr, got empty")
	}

	// 2. galaxy manifest Notice-only success -> null
	var stdoutManifest, stderrManifest bytes.Buffer
	code = runWithDeps([]string{"galaxy", "manifest", "nomatch", "--json"}, strings.NewReader(""), &stdoutManifest, &stderrManifest, deps)
	if code != 0 {
		t.Fatalf("galaxy manifest exit = %d, want 0 (stderr: %s)", code, stderrManifest.String())
	}
	if strings.TrimSpace(stdoutManifest.String()) != "null" {
		t.Errorf("galaxy manifest notice-only payload = %q, want \"null\"", stdoutManifest.String())
	}
	if stderrManifest.Len() == 0 {
		t.Error("galaxy manifest notice-only expected notice in stderr, got empty")
	}

	// 3. galaxy cdns Notice-only success -> []
	var stdoutCDNs, stderrCDNs bytes.Buffer
	code = runWithDeps([]string{"galaxy", "cdns", "nomatch", "--json"}, strings.NewReader(""), &stdoutCDNs, &stderrCDNs, deps)
	if code != 0 {
		t.Fatalf("galaxy cdns exit = %d, want 0 (stderr: %s)", code, stderrCDNs.String())
	}
	var cdnsPayload []any
	if err := jsonv2.Unmarshal(stdoutCDNs.Bytes(), &cdnsPayload); err != nil {
		t.Fatalf("galaxy cdns stdout is not a valid JSON array: %v\nStdout: %s", err, stdoutCDNs.String())
	}
	if len(cdnsPayload) != 0 {
		t.Errorf("galaxy cdns notice-only payload = %v, want []", cdnsPayload)
	}
	if stderrCDNs.Len() == 0 {
		t.Error("galaxy cdns notice-only expected notice in stderr, got empty")
	}
}

// TestJSONErrorExitCodeAndStderr verifies that under --json, an operational failure
// routes the error to stderr and exits non-zero without writing invalid JSON to stdout.
func TestJSONErrorExitCodeAndStderr(t *testing.T) {
	server, deps := setupJSONTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	// Querying non-existent game ID 999999999 triggers operational failure
	code := runWithDeps([]string{"game", "999999999", "--json"}, strings.NewReader(""), &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("game 999999999 --json exit = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Error("expected error message on stderr, got empty")
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout on operation error, got %q", stdout.String())
	}
}

// TestWindowsNativePathsInCLIOutput verifies that on Windows runtime, all CLI
// path representations use native backslashes and contain zero mixed forward slashes.
func TestWindowsNativePathsInCLIOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native path assertions only apply to windows GOOS")
	}

	// 1. Orphans display paths: relative paths must use native backslashes
	res := core.OrphansResult{
		InstallPath: `C:\Games\HoMM3`,
		Files: []string{
			`C:\Games\HoMM3\saves\autosave.gam`,
			`C:\Games\HoMM3\mods\hd\patch.dll`,
		},
	}
	var orphansOut bytes.Buffer
	renderOrphans(&orphansOut, res)
	orphansOutput := orphansOut.String()
	if strings.Contains(orphansOutput, "saves/autosave.gam") || strings.Contains(orphansOutput, "mods/hd/patch.dll") {
		t.Errorf("renderOrphans contains forward slashes on Windows: %q", orphansOutput)
	}
	if !strings.Contains(orphansOutput, `saves\autosave.gam`) || !strings.Contains(orphansOutput, `mods\hd\patch.dll`) {
		t.Errorf("renderOrphans missing native backslashes on Windows: %q", orphansOutput)
	}

	// 2. Verify display paths: root and destination must use native backslashes with zero mixed slashes
	vRes := core.VerifyResult{
		InstallPath: `C:\Games\HoMM3`,
		Facts: []core.FileFact{
			{Destination: `C:\Games\HoMM3\Data\H3bitmap.lod`, Status: reconcile.StatusND},
		},
	}
	var vOut, vErr bytes.Buffer
	renderVerify(&vOut, &vErr, vRes)
	vOutput := vOut.String()
	if strings.Contains(vOutput, "/") {
		t.Errorf("renderVerify output contains forward slashes on Windows: %q", vOutput)
	}
	if !strings.Contains(vOutput, `Verifying → C:\Games\HoMM3`) || !strings.Contains(vOutput, `C:\Games\HoMM3\Data\H3bitmap.lod`) {
		t.Errorf("renderVerify output missing expected native paths: %q", vOutput)
	}

	// 3. Install display paths: installPath and task destination must use native backslashes with zero mixed slashes
	installRoot := `C:\Games`
	gameDir := "Heroes of Might and Magic 3"
	planInstallPath := filepath.Join(installRoot, gameDir)
	planMsg := "Installing → " + planInstallPath
	if strings.Contains(planMsg, "/") {
		t.Errorf("install plan message contains forward slashes on Windows: %q", planMsg)
	}
	if planMsg != `Installing → C:\Games\Heroes of Might and Magic 3` {
		t.Errorf("install plan message = %q, want native backslashes", planMsg)
	}
	taskDest := filepath.Join(planInstallPath, filepath.FromSlash("Data/H3bitmap.lod"))
	if strings.Contains(taskDest, "/") {
		t.Errorf("task destination contains forward slashes on Windows: %q", taskDest)
	}
	if taskDest != `C:\Games\Heroes of Might and Magic 3\Data\H3bitmap.lod` {
		t.Errorf("task destination = %q, want native backslashes", taskDest)
	}

	// 4. compactPath display: mixed input must resolve to pure native backslashes
	compacted := compactPath(`C:\Games/Heroes 3/Data/Maps/map.h3m`, 25)
	if strings.Contains(compacted, "/") {
		t.Errorf("compactPath output contains forward slashes on Windows: %q", compacted)
	}
	if compacted != `…\Data\Maps\map.h3m` {
		t.Errorf("compactPath = %q, want …\\Data\\Maps\\map.h3m", compacted)
	}

	// 5. gamedetails local path: GetFilepath must use native backslashes with zero mixed slashes
	gf := gamedetails.GameFile{Gamename: "homm3", Path: "setup_homm3.exe", Platform: config.PlatformWindows, Type: config.GFBaseInstaller}
	conf := config.DirectoryConfig{Directory: `C:\Games`, SubDirectories: true, GameSubdir: "%gamename%", InstallersSubdir: "%platform%"}
	gd := gamedetails.GameDetails{Installers: []gamedetails.GameFile{gf}}
	gd.MakeFilepaths(conf)
	path := gd.Installers[0].GetFilepath()
	if strings.Contains(path, "/") {
		t.Errorf("gamedetails GetFilepath contains forward slashes on Windows: %q", path)
	}
	if path != `C:\Games\homm3\windows\setup_homm3.exe` {
		t.Errorf("gamedetails GetFilepath = %q, want C:\\Games\\homm3\\windows\\setup_homm3.exe", path)
	}
}
