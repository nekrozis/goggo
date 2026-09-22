package core

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/manifest/gogxml"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
)

// Assembly tests: the batch chain, the single-file chain, the aggregate exit
// verdicts and the two checksum policies. Everything runs against the
// acquisition fixture with the transport as the only double.

// websiteConfigIn builds the config one website run consumes: the acquisition
// defaults plus the directory layout the CLI would have applied (the six subdir
// fields at their configured defaults - core reads them, it does not invent
// them).
func websiteConfigIn(t *testing.T) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gameInfoConfigIn(t, dir)
	cfg.Directories.Directory = dir + "/"
	cfg.Directories.SubDirectories = true
	cfg.Directories.GameSubdir = "%gamename%"
	cfg.Directories.InstallersSubdir = ""
	cfg.Directories.ExtrasSubdir = "extras"
	cfg.Directories.PatchesSubdir = "patches"
	cfg.Directories.LanguagePackSubdir = "languagepacks"
	cfg.Directories.DLCSubdir = "dlc/%dlcname%"
	return cfg, dir
}

// oneProductFixture serves base game 100 (installer + extras) with owned DLC
// 200 (one installer), and the bytes for every file named serve.
func oneProductFixture(t *testing.T, serve ...string) *gameInfoFixture {
	t.Helper()
	f := newGameInfoFixture(t)
	f.setProduct("100", gameInfoDoc("100", "base_game", "Base Game",
		windowsInstaller("base.exe"), []string{gameInfoNode("sound.mp3", "", "")},
		[]string{"200"}, f.URL+"/dlc-expanded"))
	f.setExpanded(gameInfoDoc("200", "base_game_dlc", "Base Game DLC",
		windowsInstaller("dlc.exe"), nil, nil, ""))
	f.setOwned("200")
	// The name lookup the file specs use resolves through the account list;
	// catalog matches the GameRegex against the slug.
	f.setList(`{"id":100,"slug":"base_game","title":"Base Game"}`)
	for _, name := range serve {
		f.setFile(name, "bytes-of-"+name)
	}
	return f
}

func TestDownloadWebsiteBatchAssemblesAndRuns(t *testing.T) {
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, dir := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	// The recursive vector is what makes the DLC installer part of the queue:
	// base installer + base extra + dlc installer.
	if res.Tasks != 3 {
		t.Errorf("tasks = %d, want the base files plus the DLC file", res.Tasks)
	}
	if res.TotalSize != 30 {
		t.Errorf("total size = %d, want 3x10", res.TotalSize)
	}
	if res.Failed() {
		t.Errorf("failures = %+v, want none", res.Failures)
	}

	// Layout: installers sit under the game dir (empty subdir default),
	// extras under extras/, the DLC installer under dlc/<dlcname>/.
	for path, want := range map[string]string{
		filepath.Join(dir, "base_game", "base.exe"):                        "bytes-of-base.exe",
		filepath.Join(dir, "base_game", "extras", "sound.mp3"):             "bytes-of-sound.mp3",
		filepath.Join(dir, "base_game", "dlc", "base_game_dlc", "dlc.exe"): "bytes-of-dlc.exe",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", path, body, want)
		}
	}
}

// TestDownloadWebsiteTypeIntentFiltersBothFaces is the DEFECT-TYPE1 behaviour
// guard: a request-carried mask must reach the queue (G1a) and the acquisition
// (G1b), not just the Parse face. G1b asserts through the owned-games endpoint
// because that is where the acquisition's mask is observable independently of
// the queue filter: a mask without DLC bits must not ask the account who owns
// what.
func TestDownloadWebsiteTypeIntentFiltersBothFaces(t *testing.T) {
	t.Run("G1a queue filtering", func(t *testing.T) {
		f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
		cfg, dir := websiteConfigIn(t)
		d := newGameInfoDownloader(t, f, cfg)

		extra := uint32(config.GFExtra)
		res, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{
			Products: []string{"100"}, RefMode: ProductRefExact, Include: &extra,
		})
		if err != nil {
			t.Fatalf("DownloadWebsite: %v", err)
		}
		if res.Tasks != 1 {
			t.Errorf("tasks = %d, want only the base extra", res.Tasks)
		}
		if res.TotalSize != 10 {
			t.Errorf("total size = %d, want the extras-only sum", res.TotalSize)
		}
		if body, err := os.ReadFile(filepath.Join(dir, "base_game", "extras", "sound.mp3")); err != nil || string(body) != "bytes-of-sound.mp3" {
			t.Errorf("extra download missing: %v", err)
		}
		for _, absent := range []string{
			filepath.Join(dir, "base_game", "base.exe"),
			filepath.Join(dir, "base_game", "dlc", "base_game_dlc", "dlc.exe"),
		} {
			if _, err := os.Stat(absent); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("installer entered the queue under --type extras: %s", absent)
			}
		}
	})

	t.Run("G1b acquisition filtering", func(t *testing.T) {
		ownedPath := "/www/user/data/games"
		// A mask without any DLC bit must not trigger the owned-games fetch:
		// the acquisition consumed the request intent, not the configured all.
		f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
		cfg, _ := websiteConfigIn(t)
		d := newGameInfoDownloader(t, f, cfg)
		baseExtra := uint32(config.GFBaseExtra)
		if _, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{
			Products: []string{"100"}, RefMode: ProductRefExact, Include: &baseExtra,
		}); err != nil {
			t.Fatalf("DownloadWebsite: %v", err)
		}
		if got := f.seen(ownedPath); got != 0 {
			t.Errorf("owned-games fetched %d times under a DLC-free mask, want 0 (acquisition ignored req.Include)", got)
		}

		// The same run without the intent falls back to the configured mask,
		// which carries DLC bits: the fetch must happen. This pins the nil
		// semantics of effectiveInclude and kills a mutation that drops the
		// fallback.
		f2 := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
		d2 := newGameInfoDownloader(t, f2, cfg)
		if _, err := d2.DownloadWebsite(context.Background(), WebsiteDownloadRequest{
			Products: []string{"100"}, RefMode: ProductRefExact,
		}); err != nil {
			t.Fatalf("DownloadWebsite (nil Include): %v", err)
		}
		if got := f2.seen(ownedPath); got == 0 {
			t.Errorf("owned-games not fetched under the configured all-mask, want the fallback to fire")
		}
	})
}

// TestDownloadWebsiteAggregateKeepsRunningAndReports locks the aggregate exit
// contract on the batch chain: a failing task in the MIDDLE of the queue does
// not stop the tasks after it, the successful downloads are kept, and the
// verdict is a failure the CLI maps to exit 1.
func TestDownloadWebsiteAggregateKeepsRunningAndReports(t *testing.T) {
	// sound.mp3 (the queue's middle entry) is never served: its task 404s.
	f := oneProductFixture(t, "base.exe", "dlc.exe")
	cfg, dir := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if !res.Failed() {
		t.Fatal("aggregate = success, want the failed task reported")
	}
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0].Destination, "sound.mp3") {
		t.Errorf("failures = %+v, want exactly the sound.mp3 task", res.Failures)
	}
	for _, rel := range []string{
		filepath.Join("base_game", "base.exe"),
		filepath.Join("base_game", "dlc", "base_game_dlc", "dlc.exe"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("the download %s after/before the failure was not kept: %v", rel, err)
		}
	}
}

func TestDownloadWebsiteFreeSpaceGate(t *testing.T) {
	f := newGameInfoFixture(t)
	// One installer whose API size is a petabyte: the gate must speak before
	// the queue runs.
	f.setProduct("100", `{"id":100,"slug":"big_game","title":"Big",`+
		`"downloads":{"installers":[{"name":"i","version":"1","count":1,"total_size":1099511627776,`+
		`"files":[{"id":"big.exe","downlink":"https://api.gog.com/dl/big.exe","size":1099511627776}],"os":"windows","language":"en"}],`+
		`"bonus_content":[],"patches":[],"language_packs":[]}}`)
	cfg, _ := websiteConfigIn(t)
	cfg.DownloadConfig.FreeSpaceCheck = true
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("err = %v, want the free-space refusal", err)
	}
	if res.TotalSize != 1099511627776 {
		t.Errorf("total size = %d, want it reported with the refusal", res.TotalSize)
	}
	if f.count("/games/some-game/big.exe") != 0 {
		t.Error("the queue must not run once the gate refused it")
	}
}

func TestParseWebsiteFileSpec(t *testing.T) {
	cases := []struct {
		spec              string
		game, dlc, fileid string
		wantErr           bool
	}{
		{"base_game/12", "base_game", "", "12", false},
		{"base_game/dlc_one/34", "base_game", "dlc_one", "34", false},
		{"gogdownloader://base_game/12", "base_game", "", "12", false},
		{"gogdownloader://a/b/c", "a", "b", "c", false},
		{"nosep", "", "", "", true},
		{"a/b/c/d", "", "", "", true},
		{"a//b", "", "", "", true},
		{"/a/b", "", "", "", true},
		{"a/b/", "", "", "", true},
	}
	for _, tc := range cases {
		game, dlc, fileid, err := parseWebsiteFileSpec(tc.spec)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseWebsiteFileSpec(%q) = ok, want refusal", tc.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWebsiteFileSpec(%q): %v", tc.spec, err)
			continue
		}
		if game != tc.game || dlc != tc.dlc || fileid != tc.fileid {
			t.Errorf("parseWebsiteFileSpec(%q) = %q/%q/%q, want %q/%q/%q",
				tc.spec, game, dlc, fileid, tc.game, tc.dlc, tc.fileid)
		}
	}
}

// TestParseWebsiteSize locks the API size string's contract: the digits are
// read after trimming, and a form with no size in it — unparsable or negative
// — counts as zero rather than making the free-space gate stricter than "no
// size".
func TestParseWebsiteSize(t *testing.T) {
	cases := []struct {
		size string
		want int64
	}{
		{"123", 123},
		{" 42 ", 42},
		{"0", 0},
		{"", 0},
		{"not-a-number", 0},
		{"-5", 0},
		{" -5 ", 0},
	}
	for _, tc := range cases {
		if got := parseWebsiteSize(tc.size); got != tc.want {
			t.Errorf("parseWebsiteSize(%q) = %d, want %d", tc.size, got, tc.want)
		}
	}
}

func TestDownloadWebsiteFilesChain(t *testing.T) {
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, dir := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	// Base file, an unknown id in the MIDDLE, then a DLC file (three parts)
	// and a protocol-prefixed spec: the failure must not stop the specs that
	// follow it, and the good downloads must stay on disk — the ordering is
	// what kills a "break on first failure" mutation.
	res := d.DownloadWebsiteFiles(context.Background(), []string{
		"base_game/base.exe",
		"base_game/no_such_id",
		"base_game/base_game_dlc/dlc.exe",
		// The extras entry carries the numeric wire id: the lookup matches the
		// converted string form.
		"gogdownloader://base_game/13403",
	}, "", ProductRefExact)
	if !res.Failed() {
		t.Fatal("aggregate = success, want the unknown id reported")
	}
	if len(res.Outcomes) != 4 {
		t.Fatalf("outcomes = %d, want one per spec", len(res.Outcomes))
	}
	if res.Outcomes[0].Err != nil {
		t.Errorf("spec 0 = %v, want success", res.Outcomes[0].Err)
	}
	if res.Outcomes[1].Err == nil || !strings.Contains(res.Outcomes[1].Err.Error(), "Failed to find file info") {
		t.Errorf("unknown id outcome = %v, want the refusal", res.Outcomes[1].Err)
	}
	for i := 2; i < 4; i++ {
		if res.Outcomes[i].Err != nil {
			t.Errorf("spec %d after the failure = %v, want success", i, res.Outcomes[i].Err)
		}
	}
	for _, rel := range []string{
		filepath.Join("base_game", "base.exe"),
		filepath.Join("base_game", "dlc", "base_game_dlc", "dlc.exe"),
		filepath.Join("base_game", "extras", "sound.mp3"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s not downloaded: %v", rel, err)
		}
	}
}

func TestDownloadWebsiteFileOutputOverrideAndNumericID(t *testing.T) {
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, dir := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	out := filepath.Join(dir, "renamed.mp3")
	res := d.DownloadWebsiteFiles(context.Background(), []string{"base_game/13403"}, out, ProductRefExact)
	if res.Failed() {
		t.Fatalf("numeric id spec = %+v, want success", res.Outcomes)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read -o target: %v", err)
	}
	if string(body) != "bytes-of-sound.mp3" {
		t.Errorf("-o target = %q, want the extras bytes", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "base_game", "extras", "sound.mp3")); !os.IsNotExist(err) {
		t.Error("-o must not also fill the derived destination")
	}
}

// TestChecksumDualPolicy is the reverse of the batch policy: the SAME extras
// downlink document, carrying a checksum url, is read on the single-file
// policy and ignored on the batch policy.
func TestChecksumDualPolicy(t *testing.T) {
	const downlinkDoc = `{"downlink":"https://cdn.example.com/file.bin","checksum":"%s/checksum"}`
	const checksumDoc = `<file name="file.bin" md5="abc" total_size="12"/>`

	for _, tc := range []struct {
		name     string
		policy   checksumPolicy
		task     model.WebsiteTask
		wantHits int
	}{
		{
			"batch extras ignores the checksum",
			checksumGated,
			model.WebsiteTask{Destination: "file.bin", Gamename: "game", Extra: true},
			0,
		},
		{
			"single file reads the checksum whatever the type",
			checksumAlways,
			model.WebsiteTask{Destination: "file.bin", Gamename: "game", Extra: true},
			1,
		},
		{
			"batch installer still reads it under the gate",
			checksumGated,
			model.WebsiteTask{Destination: "file.bin", Gamename: "game", Checksummed: true},
			1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProviderFixture(t)
			f.set("/downlink", fmt.Sprintf(downlinkDoc, f.URL))
			f.set("/checksum", checksumDoc)
			refreshes := &atomic.Int32{}
			p := newProviderWithPolicy(t, true, refreshes, tc.policy)
			task := tc.task
			task.DownlinkURL = f.url("/downlink")
			_, xml, err := p.Resolve(context.Background(), task)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if hits := f.hitCount("/checksum"); hits != tc.wantHits {
				t.Errorf("checksum requests = %d, want %d", hits, tc.wantHits)
			}
			if tc.wantHits == 1 && xml != checksumDoc {
				t.Errorf("checksum xml = %q, want the served document", xml)
			}
			if tc.wantHits == 0 && xml != "" {
				t.Errorf("checksum xml = %q, want none", xml)
			}
		})
	}
}

// TestChecksumAlwaysToleratesFetchFailure locks the single-file chain's
// warning-not-failure semantics: an unreachable
// checksum document downloads the file anyway, with no document.
func TestChecksumAlwaysToleratesFetchFailure(t *testing.T) {
	f := newProviderFixture(t)
	f.set("/downlink", `{"downlink":"https://cdn.example.com/file.bin","checksum":"`+f.URL+`/gone"}`)
	refreshes := &atomic.Int32{}
	p := newProviderWithPolicy(t, false, refreshes, checksumAlways)
	url, xml, err := p.Resolve(context.Background(), model.WebsiteTask{
		Destination: "file.bin", DownlinkURL: f.url("/downlink"), Gamename: "game",
	})
	if err != nil {
		t.Fatalf("Resolve = %v, want the download to survive", err)
	}
	if url != "https://cdn.example.com/file.bin" || xml != "" {
		t.Errorf("Resolve = %q/%q", url, xml)
	}
}

// TestWebsiteTaskMapping locks the field-for-field conversion and the two
// behaviour flags: Checksummed is installer-or-patch, Extra is
// extras, whatever the API documents carry.
func TestWebsiteTaskMapping(t *testing.T) {
	task := websiteTaskFor(gamedetails.GameFile{
		Gamename: "game", ID: "id1", Size: "123",
		GalaxyDownlinkJSONURL: "https://api/dl", Type: config.GFDLCInstaller,
	})
	if !task.Checksummed || task.Extra {
		t.Errorf("dlc installer flags = %+v, want Checksummed only", task)
	}
	if task.Gamename != "game" || task.Size != "123" || task.DownlinkURL != "https://api/dl" {
		t.Errorf("field mapping = %+v", task)
	}
	task = websiteTaskFor(gamedetails.GameFile{Gamename: "game", ID: "id2", Type: config.GFBaseExtra})
	if task.Checksummed || !task.Extra {
		t.Errorf("extras flags = %+v, want Extra only", task)
	}
	task = websiteTaskFor(gamedetails.GameFile{Gamename: "game", ID: "id3", Type: config.GFBasePatch})
	if !task.Checksummed {
		t.Error("patch must be Checksummed")
	}
}

// TestDownloadWebsiteRefreshFailureIsNotSwallowed locks the other half of the
// refresh contract at the acquisition boundary: a dead credential path fails
// the command with its reason, it is never reported as "0 tasks, success". The
// per-task refresh verdict on the transfer side is the provider test's subject.
func TestDownloadWebsiteRefreshFailureIsNotSwallowed(t *testing.T) {
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, _ := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)
	// Expire the token and break the refresh endpoint: the run cannot even
	// acquire, and the reason must travel.
	d.token.StoreLoginResponse(map[string]any{"access_token": "a", "expires_at": 1})
	f.setFailure("/token", 500)

	_, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("err = %v, want the refresh reason", err)
	}
}

// TestDownloadWebsiteCancellationTravels locks the separation of cancellation
// from task failure: a cancelled run returns the context error, which the CLI
// maps to 130 — it is never an aggregate of per-task failures.
func TestDownloadWebsiteCancellationTravels(t *testing.T) {
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, _ := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.DownloadWebsite(ctx, WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestProviderRefreshFailureIsAnError locks the transfer-side verdict path: a
// refresh failure is the task's operational error (fail → TaskResult), reported
// with its reason — the batch chain keeps failing on it.
func TestProviderRefreshFailureIsAnError(t *testing.T) {
	f := newProviderFixture(t)
	f.set("/downlink", `{"downlink":"https://cdn.example.com/f.bin"}`)
	p := &websiteURLProvider{
		refresh: tokenRefresher{
			refresh: func(context.Context) error { return fmt.Errorf("HTTP 500") },
			expired: func() bool { return true },
		},
	}
	_, _, err := p.Resolve(context.Background(), model.WebsiteTask{DownlinkURL: f.url("/downlink")})
	if err == nil || !strings.Contains(err.Error(), "refresh login") {
		t.Fatalf("Resolve = %v, want the refresh reason", err)
	}
}

// --- XML1: the automatic manifest and the offline skip ledger -------------

func coreMD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestBackupDownloadCreateXMLAtomicFallback locks both halves of DEC-XML-2:
// a successful run leaves a valid manifest beside the cache layout, and a run
// whose manifest cannot be written keeps the downloaded file and fails the
// task — the bytes the user asked for are never traded for the derived record.
func TestBackupDownloadCreateXMLAtomicFallback(t *testing.T) {
	// Success: the installer has no checksum document in this fixture, so the
	// generated one is the file's manifest.
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg, dir := websiteConfigIn(t)
	cfg.DownloadConfig.CreateXML = true
	d := newGameInfoDownloader(t, f, cfg)
	if _, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact}); err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	docPath := filepath.Join(cfg.XMLDirectory, "base_game", "base.exe.xml")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("generated manifest: %v", err)
	}
	doc, perr := gogxml.Parse(bytes.NewReader(data))
	if perr != nil {
		t.Fatalf("generated manifest is not valid: %v", perr)
	}
	body, err := os.ReadFile(filepath.Join(dir, "base_game", "base.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "base.exe" || doc.TotalSize != int64(len(body)) || doc.MD5 != coreMD5(string(body)) {
		t.Errorf("manifest = %s/%d/%s, want the downloaded file's facts", doc.Name, doc.TotalSize, doc.MD5)
	}

	// Failure: an XML directory that cannot exist fails the task, keeps the
	// downloaded file, and leaves no partial manifest behind.
	f2 := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg2, dir2 := websiteConfigIn(t)
	cfg2.DownloadConfig.CreateXML = true
	blocked := filepath.Join(dir2, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2.XMLDirectory = blocked
	d2 := newGameInfoDownloader(t, f2, cfg2)
	res, err := d2.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if !res.Failed() {
		t.Fatal("run succeeded, want the manifest failure to reach the verdict")
	}
	if _, err := os.Stat(filepath.Join(dir2, "base_game", "base.exe")); err != nil {
		t.Errorf("downloaded file was not kept: %v", err)
	}
	if !strings.Contains(res.Failures[0].Err.Error(), "Failed to create directory") {
		t.Errorf("failure = %v, want the manifest write failure", res.Failures[0].Err)
	}
}

// TestBackupDownloadNoRemoteXMLStatusContract locks the four-state evidence
// model on the batch chain: a skip justified by a cached manifest and a skip
// justified by nothing but a size comparison must not share a label.
func TestBackupDownloadNoRemoteXMLStatusContract(t *testing.T) {
	const extrasBody = "bytes-of-sound.mp3"

	// Size-only: the extras file is on disk, no manifest exists anywhere, and
	// the content-length probe matches. The run may skip it, but the ledger
	// must say the chunks were never looked at. The served bytes are the same
	// length with different content, so a download would be visible in the
	// file itself — the only request the skip may make is the HEAD probe.
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	f.setFile("sound.mp3", strings.Repeat("X", len(extrasBody)))
	cfg, dir := websiteConfigIn(t)
	cfg.DownloadConfig.RemoteXML = false
	extras := filepath.Join(dir, "base_game", "extras", "sound.mp3")
	if err := os.MkdirAll(filepath.Dir(extras), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extras, []byte(extrasBody), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newGameInfoDownloader(t, f, cfg)
	res, err := d.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Evidence != transfer.SkipSizeOnly {
		t.Fatalf("skipped = %+v, want exactly the extras file as size-only", res.Skipped)
	}
	if !strings.HasSuffix(res.Skipped[0].Destination, "sound.mp3") {
		t.Errorf("skipped %s, want the extras file", res.Skipped[0].Destination)
	}
	if body, err := os.ReadFile(extras); err != nil || string(body) != extrasBody {
		t.Errorf("extras file was rewritten (%q, %v), want the untouched original", body, err)
	}
	if hits := f.count("/games/some-game/sound.mp3"); hits != 1 {
		t.Errorf("requests for the extras file = %d, want only the size probe", hits)
	}

	// Manifest: the same file with a valid cached document is a
	// manifest-backed skip — a different evidence value, not the same label.
	f2 := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	cfg2, dir2 := websiteConfigIn(t)
	cfg2.DownloadConfig.RemoteXML = false
	extras2 := filepath.Join(dir2, "base_game", "extras", "sound.mp3")
	if err := os.MkdirAll(filepath.Dir(extras2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extras2, []byte(extrasBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(cfg2.XMLDirectory, "base_game")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`<file name="sound.mp3" chunks="1" total_size="%d" md5="%s"><chunk id="0" from="0" to="%d" method="md5">%s</chunk></file>`,
		len(extrasBody), coreMD5(extrasBody), len(extrasBody)-1, coreMD5(extrasBody))
	if err := os.WriteFile(filepath.Join(cache, "sound.mp3.xml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	d2 := newGameInfoDownloader(t, f2, cfg2)
	res2, err := d2.DownloadWebsite(context.Background(), WebsiteDownloadRequest{Products: []string{"100"}, RefMode: ProductRefExact})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if len(res2.Skipped) != 1 || res2.Skipped[0].Evidence != transfer.SkipVerifiedManifest {
		t.Fatalf("skipped = %+v, want the extras file as manifest-backed", res2.Skipped)
	}
}
