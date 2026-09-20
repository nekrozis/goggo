package core

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// The bytes behind a verify fixture's hashes: the ordinary file, the file that
// lives inside the small-files container, the container's own payload and the
// DLC's file. The fixture declares their REAL sizes and hashes, so a test
// produces each observed fact by writing them, editing them or leaving them
// out — never by faking a hash string.
var (
	verifyPlain   = []byte("plain file content")
	verifyMember  = []byte("member content")
	verifyDLC     = []byte("dlc file content")
	verifyPayload = []byte("container payload")
)

func verifyMD5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// verifyChunk is one uncompressed chunk holding exactly content.
func verifyChunk(content []byte) string {
	size := strconv.Itoa(len(content))
	return `{"compressedMd5":"unused","md5":"` + verifyMD5(content) +
		`","compressedSize":` + size + `,"size":` + size + `}`
}

// The base depot's two items, as JSON, so a test can add one of its own.
func verifyPlainItem() string {
	return `{"path":"game/data.bin","md5":"` + verifyMD5(verifyPlain) + `",` +
		`"chunks":[` + verifyChunk(verifyPlain) + `]}`
}

func verifyMemberItem() string {
	return `{"path":"game/small1.txt","md5":"` + verifyMD5(verifyMember) + `",` +
		`"sfcRef":{"offset":0,"size":` + strconv.Itoa(len(verifyMember)) + `},` +
		`"chunks":[` + verifyChunk(verifyMember) + `]}`
}

// verifyBadMemberItem is a member whose destination the filesystem refuses: the
// path carries a NUL byte, which every platform rejects. It is a member and not
// an ordinary file on purpose — the plan never observes the members (they arrive
// by extraction), so the failure reaches the verification's own classification
// instead of failing the plan first. That is what makes it a usable injection.
func verifyBadMemberItem() string {
	return `{"path":"game/bad\u0000.bin","md5":"` + verifyMD5([]byte("x")) + `",` +
		`"sfcRef":{"offset":0,"size":1},"chunks":[` + verifyChunk([]byte("x")) + `]}`
}

// verifyFixture is the plan fixture with known content behind its hashes: one
// ordinary file, one small-files member, the container that carries it and one
// DLC file. Dependencies are off, so the expected set is exactly these files.
type verifyFixture struct {
	*planFixture
	cfg  config.Config
	root string
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	f := &verifyFixture{planFixture: newPlanFixture(t)}
	f.cfg = planTestConfig(t)
	f.cfg.DownloadConfig.GalaxyDependencies = false
	f.root = f.cfg.Directories.Directory + "W3 GOTY"

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"}]}`)
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],`+
			`"manifest":"`+planDepotHashLang+`"},`+
			`{"productId":"`+planDLCProductID+`","languages":["en-US"],"osBitness":["64"],`+
			`"manifest":"`+planDepotHashDLC+`"}]}`)
	f.setBaseDepot()
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashDLC),
		`{"depot":{"items":[{"path":"game/dlc.bin","md5":"`+verifyMD5(verifyDLC)+`",`+
			`"chunks":[`+verifyChunk(verifyDLC)+`]}]}}`)
	return f
}

// setBaseDepot serves the base depot with the ordinary file, the container and
// the member, plus whatever the caller adds.
func (f *verifyFixture) setBaseDepot(extraItems ...string) {
	items := append([]string{verifyPlainItem(), verifyMemberItem()}, extraItems...)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{`+
			`"smallFilesContainer":{"chunks":[`+verifyChunk(verifyPayload)+`]},`+
			`"items":[`+strings.Join(items, ",")+`]}}`)
}

func (f *verifyFixture) downloader(t *testing.T) *Downloader {
	t.Helper()
	return newOfflineDownloader(t, f.Server, f.cfg, newFakeConsole())
}

// place writes one expected file into the installation.
func (f *verifyFixture) place(t *testing.T, relative string, content []byte) {
	t.Helper()
	path := f.root + "/" + relative
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// facts reduces a result to "relative path → status", so a test states the
// observed state of the tree instead of its order.
func (f *verifyFixture) facts(t *testing.T, res VerifyResult) map[string]reconcile.FileStatus {
	t.Helper()
	got := map[string]reconcile.FileStatus{}
	for _, fact := range res.Facts {
		relative := strings.TrimPrefix(fact.Destination, f.root+"/")
		if fact.Err != nil {
			t.Errorf("%s: unexpected observation failure: %v", relative, fact.Err)
			continue
		}
		got[relative] = fact.Status
	}
	return got
}

// TestVerifyClassifiesTheInstallation walks the four facts over real files in a
// real installation directory: absent, exactly right, same size with different
// content, and a size that differs. The order of the facts is the plan's own
// (path order), and the small-files container is never among them — it does not
// survive an install.
func TestVerifyClassifiesTheInstallation(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, f *verifyFixture)
		want    map[string]reconcile.FileStatus
	}{
		{
			name:    "nothing installed",
			prepare: func(*testing.T, *verifyFixture) {},
			want: map[string]reconcile.FileStatus{
				"game/data.bin":   reconcile.StatusND,
				"game/dlc.bin":    reconcile.StatusND,
				"game/small1.txt": reconcile.StatusND,
			},
		},
		{
			name: "exactly right",
			prepare: func(t *testing.T, f *verifyFixture) {
				f.place(t, "game/data.bin", verifyPlain)
				f.place(t, "game/dlc.bin", verifyDLC)
				f.place(t, "game/small1.txt", verifyMember)
			},
			want: map[string]reconcile.FileStatus{
				"game/data.bin":   reconcile.StatusOK,
				"game/dlc.bin":    reconcile.StatusOK,
				"game/small1.txt": reconcile.StatusOK,
			},
		},
		{
			name: "same size, other content",
			prepare: func(t *testing.T, f *verifyFixture) {
				other := append([]byte(nil), verifyPlain...)
				other[0] = 'X'
				f.place(t, "game/data.bin", other)
				f.place(t, "game/dlc.bin", verifyDLC)
				f.place(t, "game/small1.txt", verifyMember)
			},
			want: map[string]reconcile.FileStatus{
				"game/data.bin":   reconcile.StatusMD5,
				"game/dlc.bin":    reconcile.StatusOK,
				"game/small1.txt": reconcile.StatusOK,
			},
		},
		{
			name: "size differs",
			prepare: func(t *testing.T, f *verifyFixture) {
				f.place(t, "game/data.bin", verifyPlain[:len(verifyPlain)-1])
				f.place(t, "game/dlc.bin", verifyDLC)
				f.place(t, "game/small1.txt", verifyMember)
			},
			want: map[string]reconcile.FileStatus{
				"game/data.bin":   reconcile.StatusFS,
				"game/dlc.bin":    reconcile.StatusOK,
				"game/small1.txt": reconcile.StatusOK,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			c.prepare(t, f)

			res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if res.InstallPath != f.root {
				t.Errorf("InstallPath = %q, want %q", res.InstallPath, f.root)
			}
			if got := f.facts(t, res); !sameStatuses(got, c.want) {
				t.Errorf("facts = %v, want %v", got, c.want)
			}

			// Path order, fixed by the plan, and no container: a
			// healthy installation has no container file left.
			var paths []string
			for _, fact := range res.Facts {
				relative := strings.TrimPrefix(fact.Destination, f.root+"/")
				paths = append(paths, relative)
				if strings.Contains(relative, "smallfilescontainer") {
					t.Errorf("%s is a transport artifact and must not be reported", relative)
				}
			}
			wantPaths := []string{"game/data.bin", "game/dlc.bin", "game/small1.txt"}
			if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
				t.Errorf("facts order = %v, want %v", paths, wantPaths)
			}

			// The install-shaped summary belongs to an install: a verification
			// neither prints it nor claims to be installing.
			text := planMessageTexts(PlanResult{Messages: res.Notices})
			for _, forbidden := range []string{"Installing →", "Total size installed", "Files: ", "Nothing to download"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("notices carry the install summary %q: %s", forbidden, text)
				}
			}
		})
	}
}

func sameStatuses(got, want map[string]reconcile.FileStatus) bool {
	if len(got) != len(want) {
		return false
	}
	for path, status := range want {
		if got[path] != status {
			return false
		}
	}
	return true
}

// TestVerifyKeepsThePlanDiagnostics locks what the mode does NOT drop: the plan's
// own lines — the verbose item listing above all — reach a verification, so its
// report is as auditable as an install's. Only the install-shaped summary and the
// work a verification has no use for are left out.
func TestVerifyKeepsThePlanDiagnostics(t *testing.T) {
	f := newVerifyFixture(t)
	f.cfg.MsgLevel = msgLevelVerbose

	res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	text := planMessageTexts(PlanResult{Messages: res.Notices})
	for _, want := range []string{"game/data.bin", "md5: "} {
		if !strings.Contains(text, want) {
			t.Errorf("notices are missing %q: %s", want, text)
		}
	}
}

// TestVerifyFileSetIgnoresTheContainerRoute locks the invariant the expected set
// is built on: whether a member arrives inside a small-files container or on its
// own (which is what an existing member on disk decides), the finished
// installation has the same files — so a verification and an install cannot
// disagree about which paths the installation owns.
func TestVerifyFileSetIgnoresTheContainerRoute(t *testing.T) {
	f := newVerifyFixture(t)
	d := f.downloader(t)
	req := NewInstallRequest(f.cfg, planProductID, "", ProductRefExact)

	expectedPaths := func(t *testing.T, mode planMode) []string {
		t.Helper()
		res, err := d.buildPlan(context.Background(), req, mode)
		if err != nil {
			t.Fatalf("buildPlan: %v", err)
		}
		var paths []string
		for _, file := range res.Expected {
			paths = append(paths, strings.TrimPrefix(file.Destination, f.root+"/"))
		}
		return paths
	}

	// The container route: nothing of the member is on disk, so the container
	// downloads and the member is extracted from it.
	insideContainer := expectedPaths(t, planForReadOnly)
	if len(insideContainer) != 3 {
		t.Fatalf("expected = %v, want the three files", insideContainer)
	}
	if strings.Join(insideContainer, ",") != "game/data.bin,game/dlc.bin,game/small1.txt" {
		t.Errorf("expected = %v, want path order without the container", insideContainer)
	}

	// The ordinary route: a member already on disk drops the container, and the
	// same three files are expected.
	f.place(t, "game/small1.txt", verifyMember)
	if got := expectedPaths(t, planForReadOnly); strings.Join(got, ",") != strings.Join(insideContainer, ",") {
		t.Errorf("expected after the container was dropped = %v, want %v", got, insideContainer)
	}

	// An install plans the very same set: the mode changes what is displayed
	// and what is fetched, never which files the installation owns.
	if got := expectedPaths(t, planForInstall); strings.Join(got, ",") != strings.Join(insideContainer, ",") {
		t.Errorf("install-mode expected = %v, want %v", got, insideContainer)
	}
}

// TestVerifyDoesNotFetchTheOldBuild locks what the verify mode is for: an
// install compares against the previously installed build (a second manifest
// fetch, plus the depot expansion behind it) to know what to delete; a
// verification reports facts and must not pay for that.
func TestVerifyDoesNotFetchTheOldBuild(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/goggame-"+planProductID+".info",
		[]byte(`{"buildId":"b-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDepot := galaxy.HashToGalaxyPath(planOldDepotHash)
	req := NewInstallRequest(cfg, planProductID, "", ProductRefExact)

	res, err := d.Verify(context.Background(), req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n := f.seen(oldDepot); n != 0 {
		t.Errorf("the old build's depot was fetched %d times, want none", n)
	}
	if text := planMessageTexts(PlanResult{Messages: res.Notices}); strings.Contains(text, "Deleting ") {
		t.Errorf("a verification reports no deletions: %s", text)
	}

	// The same fixture through the install path proves the assertion above is
	// about the mode and not about a fixture that cannot reach the old build.
	if _, err := d.BuildPlan(context.Background(), req); err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if n := f.seen(oldDepot); n == 0 {
		t.Error("the install path did not fetch the old build's depot: the fixture cannot prove anything")
	}
}

// TestVerifyWritesNothing locks the read-only promise:
// a verification of a tree with a mismatch in it leaves every file — content and
// modification time — exactly as it found it. Nothing is repaired, replaced or
// removed.
func TestVerifyWritesNothing(t *testing.T) {
	f := newVerifyFixture(t)
	changed := append([]byte(nil), verifyMember...)
	changed[0] = 'M'
	f.place(t, "game/data.bin", verifyPlain)
	f.place(t, "game/dlc.bin", verifyDLC)
	f.place(t, "game/small1.txt", changed)

	before := treeState(t, f.root)
	if len(before) != 3 {
		t.Fatalf("fixture placed %d files, want three", len(before))
	}

	res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := f.facts(t, res)["game/small1.txt"]; got != reconcile.StatusMD5 {
		t.Fatalf("the fixture's member should differ, got %v", got)
	}

	after := treeState(t, f.root)
	if len(after) != len(before) {
		t.Fatalf("the tree has %d files after the verification, had %d", len(after), len(before))
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("%s disappeared", path)
			continue
		}
		if got != want {
			t.Errorf("%s changed: %+v, want %+v", path, got, want)
		}
	}
}

// treeFile is one file's observable identity: its content hash and the moment it
// was last modified.
type treeFile struct {
	hash    string
	modTime time.Time
}

func treeState(t *testing.T, root string) map[string]treeFile {
	t.Helper()
	state := map[string]treeFile{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}
		state[path] = treeFile{hash: verifyMD5(data), modTime: fi.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return state
}

// TestVerifyResolvesTheSameRootAsInstall locks the shared installation-locator
// contract: for one request, a verification reads the
// same directory an install writes, custom install subdirectory included.
func TestVerifyResolvesTheSameRootAsInstall(t *testing.T) {
	for _, subdir := range []string{"%install_dir%", "My Game"} {
		t.Run(subdir, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.cfg.Directories.GalaxyInstallSubdir = subdir
			d := f.downloader(t)
			req := NewInstallRequest(f.cfg, planProductID, "", ProductRefExact)

			planned, err := d.BuildPlan(context.Background(), req)
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			verified, err := d.Verify(context.Background(), req)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if verified.InstallPath != planned.InstallPath {
				t.Errorf("verify root = %q, install root = %q", verified.InstallPath, planned.InstallPath)
			}
		})
	}
}

// TestVerifyHonoursTheMaskAndTheBlacklist locks the two filters a verification
// shares with an install: the include mask decides
// whether the DLC's files are part of the installation at all, and a blacklisted
// path is not one of its files. Both are applied while the plan is built, so a
// verification cannot see a different set than an install would.
func TestVerifyHonoursTheMaskAndTheBlacklist(t *testing.T) {
	t.Run("include mask drops the DLC", func(t *testing.T) {
		f := newVerifyFixture(t)
		f.cfg.DownloadConfig.Include &^= config.GFDLC

		res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if got := f.facts(t, res); !sameStatuses(got, map[string]reconcile.FileStatus{
			"game/data.bin":   reconcile.StatusND,
			"game/small1.txt": reconcile.StatusND,
		}) {
			t.Errorf("facts = %v, want the base game's files only", got)
		}
	})

	t.Run("blacklist drops a path", func(t *testing.T) {
		f := newVerifyFixture(t)
		blPath := filepath.Join(t.TempDir(), "blacklist.txt")
		if err := os.WriteFile(blPath, []byte("R small1\\.txt$\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		f.cfg.BlacklistFilePath = blPath

		res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		got := f.facts(t, res)
		if _, ok := got["game/small1.txt"]; ok {
			t.Errorf("a blacklisted path is not part of the installation: %v", got)
		}
		if len(got) != 2 {
			t.Errorf("facts = %v, want the two remaining files", got)
		}
	})
}

// TestVerifyReportsUnobservableFiles locks the failure path end to end: a
// destination the filesystem refuses is reported as a fact carrying the error,
// keeps its place in the report, and the walk continues — one unreadable file
// must not hide the state of the rest.
//
// The injection is a member whose path carries a NUL byte, which every platform
// rejects: the project's way of making a stat fail without asserting anything
// about permissions or locks (see the reconcile test for the same rule at the
// classification level).
func TestVerifyReportsUnobservableFiles(t *testing.T) {
	f := newVerifyFixture(t)
	f.setBaseDepot(verifyBadMemberItem())

	res, err := f.downloader(t).Verify(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("an unobservable file must not fail the run: %v", err)
	}
	if len(res.Facts) != 4 {
		t.Fatalf("facts = %d, want one per expected file", len(res.Facts))
	}

	var failed, observed int
	for _, fact := range res.Facts {
		if fact.Err == nil {
			observed++
			if fact.Status != reconcile.StatusND {
				t.Errorf("%s: status = %v, want the file to be reported absent", fact.Destination, fact.Status)
			}
			continue
		}
		failed++
		if fact.Status != reconcile.StatusUnset {
			t.Errorf("%s: status = %v, want the unset value", fact.Destination, fact.Status)
		}
		if !strings.Contains(fact.Err.Error(), "bad") {
			t.Errorf("the failure must name the file: %v", fact.Err)
		}
	}
	if failed != 1 || observed != 3 {
		t.Errorf("failed = %d, observed = %d; want one unreadable file among four", failed, observed)
	}

	// The failure keeps its place: the report stays in path order.
	var paths []string
	for _, fact := range res.Facts {
		paths = append(paths, filepath.Base(fact.Destination))
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	if strings.Join(paths, ",") != strings.Join(sorted, ",") {
		t.Errorf("facts lost their order: %v", paths)
	}
}
