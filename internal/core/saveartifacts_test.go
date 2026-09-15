package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
)

// GD5 contract tests: the three writer contracts, the fail-closed serials
// boundary, the acquisition request-count regression (C6) and the list
// read-only guarantee. C1-C6 are the Gate 1 mandated standalone assertions.

func TestWriteSerialsSkipsExisting(t *testing.T) { // C1
	dir := t.TempDir()
	path := filepath.Join(dir, "serials.txt")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := writeSerials(path, "new content", "game")
	if a.Action != ArtifactSkippedExists {
		t.Errorf("action = %v, want skipped-exists", a.Action)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "keep me" {
		t.Errorf("existing serials were overwritten: %q", body)
	}
}

func TestWriteChangelogSkipEqualOverwriteDifferent(t *testing.T) { // C2
	dir := t.TempDir()
	path := filepath.Join(dir, "changelog.html")
	if err := os.WriteFile(path, []byte("<html>same</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := writeChangelog(path, "<html>same</html>", "g"); a.Action != ArtifactSkippedUnchanged {
		t.Errorf("equal content action = %v, want skipped-unchanged", a.Action)
	}
	if a := writeChangelog(path, "<html>new</html>", "g"); a.Action != ArtifactWrote {
		t.Errorf("different content action = %v, want wrote", a.Action)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "<html>new</html>" {
		t.Errorf("changelog not overwritten: %q", body)
	}
}

func TestWriteJSONOverwritesUnconditionally(t *testing.T) { // C3
	dir := t.TempDir()
	path := filepath.Join(dir, "product_g.json")
	if err := os.WriteFile(path, []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := writeJSONFile(path, `{"fresh":1}`, ArtifactProductJSON, "g"); a.Action != ArtifactWrote {
		t.Errorf("action = %v, want wrote", a.Action)
	}
	body, _ := os.ReadFile(path)
	if string(body) != `{"fresh":1}` {
		t.Errorf("json not overwritten: %q", body)
	}
}

func TestWriteOrFailDirectoryContracts(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The DIRECT parent exists as a file ⇒ the upstream "is not directory"
	// branch (boost's exists() succeeds there). A parent under a file does
	// not exist, and there upstream prints "Failed to create directory".
	a := writeJSONFile(filepath.Join(blocker, "serials.txt"), "{}", ArtifactProductJSON, "g")
	if a.Action != ArtifactFailed || !strings.Contains(a.Err.Error(), "is not directory") {
		t.Errorf("blocked parent = %v/%v, want failed 'is not directory'", a.Action, a.Err)
	}
	if a := writeJSONFile(filepath.Join(blocker, "game", "product.json"), "{}", ArtifactProductJSON, "g"); a.Action != ArtifactFailed ||
		!strings.Contains(a.Err.Error(), "Failed to create directory") {
		t.Errorf("unreachable parent = %v/%v, want failed 'Failed to create directory'", a.Action, a.Err)
	}
	// Missing parents are created.
	gone := filepath.Join(dir, "a", "b", "serials.txt")
	if a := writeSerials(gone, "s", "g"); a.Action != ArtifactWrote {
		t.Errorf("created dirs action = %v, want wrote", a.Action)
	}
}

func TestSerialsFromCDKeyShapes(t *testing.T) { // C5
	cases := []struct {
		in, want    string
		unsupported bool
	}{
		{"ABC-123", "ABC-123\n", false},
		{"a<br>b", "a\nb\n", false},
		{"a<br/>b<br />c", "a\nb\nc\n", false},
		{"", "", false},
		{"<span>x</span>", "", true},
		{"<BR>", "<BR>\n", false}, // boost's regex is case-sensitive: not a break
	}
	for _, tc := range cases {
		got, unsupported := gamedetails.SerialsFromCDKey(tc.in)
		if got != tc.want || unsupported != tc.unsupported {
			t.Errorf("SerialsFromCDKey(%q) = %q/%v, want %q/%v", tc.in, got, unsupported, tc.want, tc.unsupported)
		}
	}
}

func TestChangelogFromJSONWrapping(t *testing.T) {
	got, err := gamedetails.ChangelogFromJSON(map[string]any{"changelog": "<p>fix</p>", "title": "Game"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, `<!DOCTYPE html>`) || !strings.Contains(got, "<title>Changelog: Game</title>") ||
		!strings.HasSuffix(got, "<body><p>fix</p></body>\n</html>") { // the C++ literal has the newline
		t.Errorf("wrapped changelog = %q", got)
	}
	if got, _ := gamedetails.ChangelogFromJSON(map[string]any{"changelog": ""}); got != "" {
		t.Errorf("empty changelog = %q, want nothing", got)
	}
	if got, _ := gamedetails.ChangelogFromJSON(map[string]any{}); got != "" {
		t.Errorf("missing changelog = %q, want nothing", got)
	}
	// The title test is presence, not value (the C++ isMember): an empty
	// title still renders "Changelog: ".
	got, _ = gamedetails.ChangelogFromJSON(map[string]any{"changelog": "c", "title": ""})
	if !strings.Contains(got, "<title>Changelog: </title>") {
		t.Errorf("present-empty title = %q, want the trailing-space form", got)
	}
}

// saveFixtureProduct serves the one-product fixture plus a details document.
func saveFixtureProduct(t *testing.T, cdKey, changelog string) (*gameInfoFixture, config.Config, string) {
	t.Helper()
	f := oneProductFixture(t, "base.exe", "sound.mp3", "dlc.exe")
	doc := `{"title":"Base Game","changelog":"` + changelog + `","cdKey":"` + cdKey + `"}`
	f.setGameDetails("100", doc)
	cfg, dir := websiteConfigIn(t)
	return f, cfg, dir
}

// TestSaveSectionFailClosedAndContracts is C4 end-to-end: a <span> cdKey
// warns and writes nothing while the changelog still lands, and the run is
// NOT an aggregate failure (the warning must not escalate, the failure must
// not hide — both halves at once).
func TestSaveSectionFailClosedAndContracts(t *testing.T) {
	f, cfg, dir := saveFixtureProduct(t, "<span>secret</span>", "<p>notes</p>")
	cfg.DownloadConfig.SaveSerials = true
	cfg.DownloadConfig.SaveChangelogs = true
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.DownloadWebsite(context.Background(), []string{"100"})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if res.Failed() {
		t.Fatalf("aggregate = failed (%+v), want the fail-closed warning not to escalate", res.Failures)
	}
	var sawFormat, sawChangelog bool
	for _, a := range res.Saved {
		switch {
		case a.Kind == ArtifactSerials && a.Action == ArtifactSkippedFormat:
			sawFormat = true
			if !strings.Contains(a.Err.Error(), "span") {
				t.Errorf("format diagnostic = %v", a.Err)
			}
		case a.Kind == ArtifactChangelog && a.Action == ArtifactWrote:
			sawChangelog = true
		}
	}
	if !sawFormat || !sawChangelog {
		t.Fatalf("artifacts = %+v, want the skipped-format serials and a written changelog", res.Saved)
	}
	if _, err := os.Stat(filepath.Join(dir, "base_game", "serials.txt")); !os.IsNotExist(err) {
		t.Error("serials.txt must not exist after the fail-closed")
	}
	body, err := os.ReadFile(filepath.Join(dir, "base_game", "changelog_base_game.html"))
	if err != nil {
		t.Fatalf("changelog: %v", err)
	}
	if !strings.Contains(string(body), "<title>Changelog: Base Game</title>") {
		t.Errorf("changelog body = %q", body)
	}
}

// TestSaveAcquisitionRequestCounting is C6, the GD5 regression lock: with
// every save flag off the details document is never requested; with any
// combination on, it is requested EXACTLY once per product (three consumers,
// one fetch).
func TestSaveAcquisitionRequestCounting(t *testing.T) {
	cases := []struct {
		name                       string
		serials, changelog, gdJSON bool
		want                       int
	}{
		{"all off", false, false, false, 0},
		{"serials only", true, false, false, 1},
		{"all three share one fetch", true, true, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, cfg, _ := saveFixtureProduct(t, "KEY-1", "cl")
			cfg.DownloadConfig.SaveSerials = tc.serials
			cfg.DownloadConfig.SaveChangelogs = tc.changelog
			cfg.DownloadConfig.SaveGameDetailsJSON = tc.gdJSON
			d := newGameInfoDownloader(t, f, cfg)
			if _, err := d.DownloadWebsite(context.Background(), []string{"100"}); err != nil {
				t.Fatal(err)
			}
			if got := f.count("/account/gameDetails"); got != tc.want {
				t.Errorf("details requests = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSaveChangelogAlreadyPresentSkipsOnSecondRun locks C1/C2 through the
// whole command: run one writes, run two skips (unchanged), and the serials
// file survives untouched even though the second run would write it again.
func TestSaveChangelogAlreadyPresentSkipsOnSecondRun(t *testing.T) {
	f, cfg, dir := saveFixtureProduct(t, "KEY-1", "cl")
	cfg.DownloadConfig.SaveSerials = true
	cfg.DownloadConfig.SaveChangelogs = true
	d := newGameInfoDownloader(t, f, cfg)

	if _, err := d.DownloadWebsite(context.Background(), []string{"100"}); err != nil {
		t.Fatal(err)
	}
	serials := filepath.Join(dir, "base_game", "serials.txt")
	if err := os.WriteFile(serials, []byte("user edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := d.DownloadWebsite(context.Background(), []string{"100"})
	if err != nil {
		t.Fatal(err)
	}
	var sawExists, sawUnchanged int
	for _, a := range res.Saved {
		switch a.Action {
		case ArtifactSkippedExists:
			sawExists++
		case ArtifactSkippedUnchanged:
			sawUnchanged++
		}
	}
	if sawExists != 1 || sawUnchanged != 1 {
		t.Errorf("second run actions: exists=%d unchanged=%d, want 1/1 (%+v)", sawExists, sawUnchanged, res.Saved)
	}
	body, _ := os.ReadFile(serials)
	if string(body) != "user edit" {
		t.Errorf("serials overwritten on the second run: %q", body)
	}
}

// TestArtifactFailureMovesExitCode locks the Gate 1 approval: a failed
// artifact continues the run, reports itself, and moves the exit verdict.
func TestArtifactFailureMovesExitCode(t *testing.T) {
	f, cfg, dir := saveFixtureProduct(t, "KEY-1", "cl")
	cfg.DownloadConfig.SaveChangelogs = true
	// Block the changelog's parent directory: base_game exists as a FILE.
	if err := os.WriteFile(filepath.Join(dir, "base_game"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newGameInfoDownloader(t, f, cfg)
	res, err := d.DownloadWebsite(context.Background(), []string{"100"})
	if err != nil {
		t.Fatalf("DownloadWebsite: %v", err)
	}
	if !res.Failed() {
		t.Error("aggregate = success, want the blocked artifact to move the verdict")
	}
	var blocked bool
	for _, a := range res.Saved {
		if a.Action == ArtifactFailed {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("no failed artifact recorded: %+v", res.Saved)
	}
}

// TestListGameDetailsIsReadOnlyAndEnumeratesAccount locks ruling 7: no
// arguments means the whole account, and the path writes nothing.
func TestListGameDetailsIsReadOnlyAndEnumeratesAccount(t *testing.T) {
	f := newGameInfoFixture(t)
	f.setProduct("100", gameInfoDoc("100", "base_game", "Base Game", windowsInstaller("base.exe"), nil, nil, ""))
	f.setList(`{"id":100,"slug":"base_game","title":"Base Game"}`)
	cfg, dir := websiteConfigIn(t)
	d := newGameInfoDownloader(t, f, cfg)

	games, err := d.ListGameDetails(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListGameDetails: %v", err)
	}
	if len(games) != 1 || games[0].Gamename != "base_game" {
		t.Fatalf("games = %+v, want the one account product", games)
	}
	if games[0].Installers[0].GetFilepath() == "" {
		t.Error("list details must derive filepaths (the text blacklist needs them)")
	}
	// Read-only: nothing under the download root, and no transfer requests.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the download root gained %d entries; list must write nothing", len(entries))
	}
	if f.count("/games/some-game") != 0 {
		t.Error("list details must not fetch file bytes")
	}
}
