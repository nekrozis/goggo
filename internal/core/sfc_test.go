package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// sfcFixture builds a plan with one container (24 bytes) and two members cut
// from it, over an install root that already holds the container file.
type sfcFixture struct {
	root      string
	container string
	body      []byte
	res       PlanResult
}

func newSFCFixture(t *testing.T) *sfcFixture {
	t.Helper()
	f := &sfcFixture{root: t.TempDir()}
	f.container = f.root + "/galaxy_smallfilescontainer_42" // the plan stores forward-slash paths
	f.body = []byte("AAAABBBBCCCCDDDDYYYYZZZZ")             // members cut from here
	if err := os.WriteFile(f.container, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.res = PlanResult{
		InstallPath: f.root,
		Plan: model.DownloadPlan{
			SFC: []model.SFCGroup{{
				Container: model.GalaxyDepotItem{Path: "galaxy_smallfilescontainer_42", ProductID: "42"},
				Items: []model.GalaxyDepotItem{
					{Path: "game/one.txt", ProductID: "42", SFCOffset: 0, SFCSize: 8},
					{Path: "game/sub/two.txt", ProductID: "42", SFCOffset: 16, SFCSize: 8},
					// A member of another product: the extraction skips it,
					// the way the product_id filter does upstream.
					{Path: "game/foreign.txt", ProductID: "99", SFCOffset: 0, SFCSize: 4},
				},
			}},
		},
	}
	return f
}

// TestExtractSmallFilesContainers locks the unpacking: members are cut from
// the container by offset and size, written fresh, and the container is
// removed. A member of another product is not cut from this container.
//
// None of these members declares a hash, which is the other half of the rule:
// without a hash there is nothing to check, so the region is cut out as it
// always was (D49 keeps that behaviour for members the manifest gives no hash).
func TestExtractSmallFilesContainers(t *testing.T) {
	f := newSFCFixture(t)
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())

	pending, err := d.ExtractSmallFilesContainers(context.Background(), f.res)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want none: no member declares a hash", pending)
	}
	assertFileContent(t, filepath.Join(f.root, "game", "one.txt"), "AAAABBBB")
	assertFileContent(t, filepath.Join(f.root, "game", "sub", "two.txt"), "YYYYZZZZ")
	if _, err := os.Stat(filepath.Join(f.root, "game", "foreign.txt")); err == nil {
		t.Error("foreign.txt was written from the wrong product's container")
	}
	assertFileAbsent(t, f.container)

	out := consoleText(t, d)
	for _, want := range []string{
		"Extracting small files container " + f.container,
		"Deleting small files container " + f.container,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// TestExtractHoldsBackAMemberItsRegionDoesNotHold is the D48/D49 core rule: a
// member whose container region does not hold its content is NOT written — the
// wrong bytes must not reach the installation — and comes back to the caller as
// a task, carrying its whole item so the direct download can use the member's
// own chunks.
func TestExtractHoldsBackAMemberItsRegionDoesNotHold(t *testing.T) {
	f := newSFCFixture(t)
	f.res.Plan.SFC[0].Items = []model.GalaxyDepotItem{
		{Path: "game/one.txt", ProductID: "42", SFCOffset: 0, SFCSize: 8,
			MD5: sfcMD5Hex([]byte("AAAABBBB")), TotalSize: 8},
		{Path: "game/broken.txt", ProductID: "42", SFCOffset: 16, SFCSize: 8,
			MD5: sfcMD5Hex([]byte("bytes the container does not hold")), TotalSize: 8},
	}
	console := newFakeConsole()
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), console)

	pending, err := d.ExtractSmallFilesContainers(context.Background(), f.res)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFileContent(t, filepath.Join(f.root, "game", "one.txt"), "AAAABBBB")
	assertFileAbsent(t, filepath.Join(f.root, "game", "broken.txt"))

	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want the one member the container cannot supply", pending)
	}
	task := pending[0]
	if task.Destination != f.root+"/game/broken.txt" || task.Item.Path != "game/broken.txt" {
		t.Errorf("pending task = %+v, want it addressed to the member", task)
	}
	// The item travels whole: the fallback must not derive anything again.
	if task.Item.MD5 != sfcMD5Hex([]byte("bytes the container does not hold")) || task.Item.TotalSize != 8 {
		t.Errorf("pending item lost its declaration: %+v", task.Item)
	}

	out := consoleText(t, d)
	if !strings.Contains(out, "(1 files)") {
		t.Errorf("the summary must count what was written: %q", out)
	}
	if !strings.Contains(console.errOut.String(), "does not match the manifest hash") {
		t.Errorf("the refusal must be reported on the error stream: %q", console.errOut.String())
	}
}

// TestExtractSkipsMissingContainer locks the exists check: a container that is
// not on disk passes over silently.
func TestExtractSkipsMissingContainer(t *testing.T) {
	f := newSFCFixture(t)
	if err := os.Remove(f.container); err != nil {
		t.Fatal(err)
	}
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())

	pending, err := d.ExtractSmallFilesContainers(context.Background(), f.res)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want none for a container that is not on disk", pending)
	}
	if strings.Contains(consoleText(t, d), "Extracting small files container") {
		t.Error("a missing container must be skipped without the extraction line")
	}
}

// newSFCBodyFixture builds a container holding body, over the members the test
// asks for. It is the flexible sibling of newSFCFixture.
func newSFCBodyFixture(t *testing.T, body string, items []model.GalaxyDepotItem) *sfcFixture {
	t.Helper()
	f := &sfcFixture{root: t.TempDir()}
	f.container = f.root + "/galaxy_smallfilescontainer_42"
	f.body = []byte(body)
	if err := os.WriteFile(f.container, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.res = PlanResult{
		InstallPath: f.root,
		Plan: model.DownloadPlan{
			SFC: []model.SFCGroup{{
				Container: model.GalaxyDepotItem{Path: "galaxy_smallfilescontainer_42", ProductID: "42"},
				Items:     items,
			}},
		},
	}
	return f
}

// TestExtractValidatesPartiallyOverlappingRegionsIndependently covers the shape
// the real container has: 1,378 of the Terraria container's regions overlap
// without being identical, because it packs unique blobs and a member's range
// can run into another member's. Each such member is judged on its own bytes —
// one member's verdict must not carry over to the other, and neither read may
// walk the offset backwards (review S9-R).
func TestExtractValidatesPartiallyOverlappingRegionsIndependently(t *testing.T) {
	const body = "0123456789AB"
	first := model.GalaxyDepotItem{
		Path: "game/a.txt", ProductID: "42", SFCOffset: 0, SFCSize: 8,
		MD5: sfcMD5Hex([]byte(body[0:8])), TotalSize: 8,
	}

	for _, c := range []struct {
		name        string
		secondMD5   string
		wantWritten bool
	}{
		{"both regions hold their own bytes", sfcMD5Hex([]byte(body[4:12])), true},
		{"the overlapping member cannot be supplied", sfcMD5Hex([]byte("bytes it does not have")), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			second := model.GalaxyDepotItem{
				Path: "game/b.txt", ProductID: "42", SFCOffset: 4, SFCSize: 8,
				MD5: c.secondMD5, TotalSize: 8,
			}
			f := newSFCBodyFixture(t, body, []model.GalaxyDepotItem{first, second})
			d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())

			pending, err := d.ExtractSmallFilesContainers(context.Background(), f.res)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			// The first member is unaffected by the second one's verdict.
			assertFileContent(t, filepath.Join(f.root, "game", "a.txt"), body[0:8])
			if c.wantWritten {
				assertFileContent(t, filepath.Join(f.root, "game", "b.txt"), body[4:12])
				if len(pending) != 0 {
					t.Errorf("pending = %+v, want none", pending)
				}
				return
			}
			assertFileAbsent(t, filepath.Join(f.root, "game", "b.txt"))
			if len(pending) != 1 || pending[0].Destination != f.root+"/game/b.txt" {
				t.Errorf("pending = %+v, want the overlapping member", pending)
			}
		})
	}
}

// TestExtractFailsAndKeepsTheContainerWhenReadingItFails locks the failure class
// the S9-R review separated out: a member whose content is wrong is repaired
// (pending + direct download), but a container that cannot be READ is an
// observation failure — the extraction cannot tell what any later member holds,
// so the install must not claim it converged, and the container must survive as
// the only copy of those bytes (decisions D43, D49).
//
// The container is a directory here: opening it succeeds, the first read fails.
// That makes the read path fail without asserting anything about permissions.
func TestExtractFailsAndKeepsTheContainerWhenReadingItFails(t *testing.T) {
	f := newSFCFixture(t)
	if err := os.Remove(f.container); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.container, 0o755); err != nil {
		t.Fatal(err)
	}
	console := newFakeConsole()
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), console)

	pending, err := d.ExtractSmallFilesContainers(context.Background(), f.res)
	if err == nil {
		t.Fatal("a container that cannot be read must fail the extraction")
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want none: nothing could be decided", pending)
	}
	if _, statErr := os.Stat(f.container); statErr != nil {
		t.Errorf("the container must survive a read failure: %v", statErr)
	}
	if strings.Contains(consoleText(t, d), "Extracting small files container") {
		t.Error("a failed extraction must not print the summary line")
	}
	assertFileAbsent(t, filepath.Join(f.root, "game", "one.txt"))
}

// failingReader hands out perRead bytes at a time and fails once limit bytes
// have been served, the way a container whose medium gives up mid-read does. The
// per-read size matters: the stream reads through a 64 KiB buffer, so a reader
// that surrenders everything at once would never fail in the middle.
type failingReader struct {
	body    []byte
	perRead int
	limit   int
	served  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.served >= r.limit {
		return 0, errReadFailed
	}
	want := r.perRead
	if want > len(p) {
		want = len(p)
	}
	if rest := len(r.body) - r.served; want > rest {
		want = rest
	}
	if want <= 0 {
		return 0, io.EOF
	}
	copy(p[:want], r.body[r.served:r.served+want])
	r.served += want
	return want, nil
}

var errReadFailed = errors.New("the container gave up mid-read")

// TestExtractTreatsAMidReadFailureAsFatal locks the same rule one level down: a
// failure that arrives after a member was already written still returns an error
// — the run stops instead of reporting the members it managed to cut out.
func TestExtractTreatsAMidReadFailureAsFatal(t *testing.T) {
	items := []model.GalaxyDepotItem{
		{Path: "game/a.txt", ProductID: "42", SFCOffset: 0, SFCSize: 4, MD5: sfcMD5Hex([]byte("0123")), TotalSize: 4},
		{Path: "game/b.txt", ProductID: "42", SFCOffset: 4, SFCSize: 4, MD5: sfcMD5Hex([]byte("4567")), TotalSize: 4},
	}
	group := model.SFCGroup{
		Container: model.GalaxyDepotItem{Path: "galaxy_smallfilescontainer_42", ProductID: "42"},
		Items:     items,
	}
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())
	root := t.TempDir()

	// Four bytes are served, then the reader fails: the first region is decided,
	// the second one never is.
	_, _, err := d.extractContainer(context.Background(),
		&failingReader{body: []byte("0123456789AB"), perRead: 4, limit: 4}, group, root)
	if err == nil {
		t.Fatal("a read failure must fail the extraction")
	}
	if !errors.Is(err, errReadFailed) {
		t.Errorf("error = %v, want the read failure itself", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "game", "a.txt")); statErr != nil {
		t.Errorf("the member read before the failure keeps its file: %v", statErr)
	}
	assertFileAbsent(t, filepath.Join(root, "game", "b.txt"))
}

// TestContainerStreamServesRegionsInOneRead locks the reader's guarantee
// (review S9-R): regions are served in ascending offset order out of one
// forward pass, so a region repeated by another member or one that starts inside
// the buffered tail costs no second read.
func TestContainerStreamServesRegionsInOneRead(t *testing.T) {
	body := []byte(strings.Repeat("abcdefgh", 8))
	reader := &countingReader{r: bytes.NewReader(body)}
	stream := newContainerStream(reader)

	for _, want := range []struct {
		offset, size uint64
		text         string
	}{
		{0, 8, "abcdefgh"},
		{0, 8, "abcdefgh"}, // the same region again: still no read
		{8, 8, "abcdefgh"},
		{16, 4, "abcd"},
		{60, 8, "efgh"}, // runs past the end: short, the way the section reader was
	} {
		got, err := stream.bytes(want.offset, want.size)
		if err != nil {
			t.Fatalf("bytes(%d, %d): %v", want.offset, want.size, err)
		}
		if string(got) != want.text {
			t.Errorf("bytes(%d, %d) = %q, want %q", want.offset, want.size, got, want.text)
		}
	}
	// No byte of the container is read twice: five regions — one of them
	// repeated — cost one data read. A second call is the probe that discovers
	// the end of the container, and it is made once.
	if reader.bytes != len(body) || reader.calls > 2 {
		t.Errorf("container read %d times for %d bytes, want one data read of %d plus at most one end probe",
			reader.calls, reader.bytes, len(body))
	}

	// The stream never walks back: a region behind the read position is refused
	// rather than re-read, which is what keeps the pass forward-only.
	if _, err := stream.bytes(0, 4); err == nil {
		t.Error("a region behind the read position must be refused")
	}
}

// TestExtractReadsAContiguousContainerOnce pins the same guarantee at the
// extraction level: 128 members covering a container end to end cost one read,
// not one per member. Without it the fix would trade a silent wrong file for
// thousands of redundant reads (review S9-R).
func TestExtractReadsAContiguousContainerOnce(t *testing.T) {
	body := bytes.Repeat([]byte("z"), 4096)
	items := make([]model.GalaxyDepotItem, 0, 128)
	for i := 0; i < 128; i++ {
		items = append(items, model.GalaxyDepotItem{
			Path:      fmt.Sprintf("game/f%03d.txt", i),
			ProductID: "42",
			SFCOffset: uint64(i * 32),
			SFCSize:   32,
		})
	}
	group := model.SFCGroup{
		Container: model.GalaxyDepotItem{Path: "galaxy_smallfilescontainer_42", ProductID: "42"},
		Items:     items,
	}
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())
	root := t.TempDir()

	reader := &countingReader{r: bytes.NewReader(body)}
	extracted, pending, err := d.extractContainer(context.Background(), reader, group, root)
	if err != nil {
		t.Fatalf("extractContainer: %v", err)
	}
	if extracted != len(items) || len(pending) != 0 {
		t.Fatalf("extracted %d, pending %d, want %d and none", extracted, len(pending), len(items))
	}
	if reader.calls != 1 || reader.bytes != len(body) {
		t.Errorf("container read %d times for %d bytes, want one read of %d", reader.calls, reader.bytes, len(body))
	}
	assertFileContent(t, filepath.Join(root, "game", "f000.txt"), strings.Repeat("z", 32))
	assertFileContent(t, filepath.Join(root, "game", "f127.txt"), strings.Repeat("z", 32))
}

// countingReader records what the extraction asked the container for: how many
// Read calls and how many bytes.
type countingReader struct {
	r     io.Reader
	calls int
	bytes int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.calls++
	c.bytes += n
	return n, err
}

// noopServer is a server the extraction never talks to.
func noopServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
}

// consoleText pulls the output the fake console captured.
func consoleText(t *testing.T, d *Downloader) string {
	t.Helper()
	c, ok := d.ui.(*fakeConsole)
	if !ok {
		t.Fatalf("ui is %T, want *fakeConsole", d.ui)
	}
	return c.out.String()
}
