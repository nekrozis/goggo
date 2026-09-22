package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/core"
)

// This file is the CLI face of XML1: the manifest search precedence and the
// create → inspect → verify loop, including the frozen JSON status names and
// the exit-code contract (0 verified, 1 not verified, 2 bad input).

// runManifestCLI drives one command line through the real dispatcher. The
// manifest commands need no session, so the zero dependencies are enough; the
// roots are isolated because `create` without -o resolves its output through
// the configuration directories.
func runManifestCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	isolateRoots(t)
	var out, errOut bytes.Buffer
	code := runWithDeps(args, strings.NewReader(""), &out, &errOut, core.Dependencies{})
	return code, out.String(), errOut.String()
}

// patternFile writes a deterministic content of the given size, so chunk
// hashes depend only on the size and on later tampering.
func patternFile(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*7 + i/256)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManifestSearchPrecedence(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pkg.bin")
	patternFile(t, target, 3<<20) // 3 MiB

	xmlDir := filepath.Join(dir, "xmldir")
	gameDir := filepath.Join(xmlDir, "mygame")
	for _, d := range []string{xmlDir, gameDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := "pkg.bin.xml"

	// Stage one valid manifest, then move it between the candidate locations.
	staged := filepath.Join(dir, "staged.xml")
	if code, out, errOut := runManifestCLI(t, "manifest", "create", target, "-o", staged); code != 0 {
		t.Fatalf("create: exit %d out=%q err=%q", code, out, errOut)
	}
	place := func(path string) {
		t.Helper()
		data, err := os.ReadFile(staged)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(path string) {
		t.Helper()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	sibling := filepath.Join(dir, base)
	gameXML := filepath.Join(gameDir, base)
	globalXML := filepath.Join(xmlDir, base)

	// An explicit --xml that misses must fail even though a valid manifest sits
	// beside the target: the explicit path is a statement, not a first guess.
	place(sibling)
	code, _, errOut := runManifestCLI(t, "manifest", "verify", target, "--xml", filepath.Join(dir, "missing.xml"))
	if code != 2 {
		t.Fatalf("explicit miss exit = %d, want 2 (err=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "explicit manifest") {
		t.Errorf("explicit miss err = %q, want it to name the explicit path failure", errOut)
	}
	if strings.Contains(errOut, "searched canonical paths") {
		t.Error("explicit miss fell back to the search chain")
	}

	// Precedence 2: the game cache, when --game names it.
	place(gameXML)
	if code, _, errOut := runManifestCLI(t, "manifest", "verify", target, "--xml-directory", xmlDir, "--game", "mygame"); code != 0 {
		t.Fatalf("game cache exit = %d, want 0 (err=%q)", code, errOut)
	}

	// Precedence 3: the global cache, when no --game is given.
	remove(gameXML)
	place(globalXML)
	if code, _, errOut := runManifestCLI(t, "manifest", "verify", target, "--xml-directory", xmlDir); code != 0 {
		t.Fatalf("global cache exit = %d, want 0 (err=%q)", code, errOut)
	}

	// Precedence 4: the sibling of the target, when the caches hold nothing.
	remove(globalXML)
	if code, _, errOut := runManifestCLI(t, "manifest", "verify", target, "--xml-directory", xmlDir); code != 0 {
		t.Fatalf("sibling exit = %d, want 0 (err=%q)", code, errOut)
	}

	// Nothing anywhere: exit 2, and the message names the search it performed.
	remove(sibling)
	code, _, errOut = runManifestCLI(t, "manifest", "verify", target, "--xml-directory", xmlDir)
	if code != 2 {
		t.Fatalf("no manifest exit = %d, want 2 (err=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "no manifest xml found") {
		t.Errorf("no manifest err = %q", errOut)
	}
}

func TestManifestCLIInspectVerifyCreateE2E(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "game.bin")
	patternFile(t, target, 3<<20)
	manifest := filepath.Join(dir, "game.bin.xml")

	// create: --chunk-size 1 over 3 MiB gives exactly three chunks.
	code, out, errOut := runManifestCLI(t, "manifest", "create", target, "--chunk-size", "1", "-o", manifest)
	if code != 0 {
		t.Fatalf("create: exit %d out=%q err=%q", code, out, errOut)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("create did not write %s: %v", manifest, err)
	}

	// inspect (text) names the manifest it read and its shape.
	code, out, errOut = runManifestCLI(t, "manifest", "inspect", manifest)
	if code != 0 {
		t.Fatalf("inspect: exit %d err=%q", code, errOut)
	}
	for _, want := range []string{"game.bin", "Total Size", "Total Chunks: 3", "File MD5"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect out missing %q:\n%s", want, out)
		}
	}

	// inspect --json follows the frozen schema: file / chunks / total_size /
	// md5 / chunk_list[].{id,from,to,method,hash}.
	code, out, _ = runManifestCLI(t, "manifest", "inspect", manifest, "--json")
	if code != 0 {
		t.Fatalf("inspect --json: exit %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("inspect --json not JSON: %v\n%s", err, out)
	}
	if doc["file"] != "game.bin" || doc["chunks"].(float64) != 3 {
		t.Errorf("inspect --json head = %v, want file/chunks", doc["file"])
	}
	if _, ok := doc["name"]; ok {
		t.Error(`inspect --json uses "name", want the schema's "file"`)
	}
	if list, ok := doc["chunk_list"].([]any); !ok || len(list) != 3 {
		t.Errorf("inspect --json chunk_list = %v", doc["chunk_list"])
	}

	// verify: intact file, exit 0, JSON status OK with every chunk ok.
	code, out, errOut = runManifestCLI(t, "manifest", "verify", target, "--xml", manifest, "--json")
	if code != 0 {
		t.Fatalf("verify: exit %d err=%q", code, errOut)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("verify --json not JSON: %v", err)
	}
	if report["status"] != "OK" || report["corrupt_chunks"].(float64) != 0 {
		t.Errorf("verify --json status = %v, want OK", report["status"])
	}
	if report["file_md5_match"] != true {
		t.Error("verify --json file_md5_match = false, want true")
	}

	// Tamper one byte in chunk 1: exit 1, CORRUPT, exactly that chunk false,
	// the remaining chunks still checked (the report is complete, not first-hit).
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	data[1<<20+10] ^= 0xFF
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runManifestCLI(t, "manifest", "verify", target, "--xml", manifest, "--json")
	if code != 1 {
		t.Fatalf("corrupt verify: exit %d, want 1 (err=%q)", code, errOut)
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("corrupt --json not JSON: %v", err)
	}
	if report["status"] != "CORRUPT" || report["corrupt_chunks"].(float64) != 1 {
		t.Errorf("corrupt status/count = %v/%v, want CORRUPT/1", report["status"], report["corrupt_chunks"])
	}
	if report["actual_md5"] == "" || report["actual_md5"] == report["expected_md5"] {
		t.Error("corrupt report must carry the recomputed file md5")
	}
	chunks, _ := report["chunks"].([]any)
	if len(chunks) != 3 {
		t.Fatalf("corrupt report chunks = %d, want the full 3", len(chunks))
	}
	for i, c := range chunks {
		if got := c.(map[string]any)["ok"].(bool); got != (i != 1) {
			t.Errorf("chunk %d ok = %v, want only chunk 1 false", i, got)
		}
	}

	// Grow the file: the size guard fires before any chunk read, and the JSON
	// reports it as the frozen SIZE_MISMATCH error branch with exit 1.
	data = append(data, 0)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runManifestCLI(t, "manifest", "verify", target, "--xml", manifest, "--json")
	if code != 1 {
		t.Fatalf("size mismatch: exit %d, want 1", code)
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("size mismatch --json not JSON: %v", err)
	}
	if report["status"] != "ERROR" || report["error_kind"] != "SIZE_MISMATCH" {
		t.Errorf("size mismatch json = %v/%v, want ERROR/SIZE_MISMATCH", report["status"], report["error_kind"])
	}

	// A syntactically valid but semantically invalid manifest is refused by
	// inspect with exit 2 — inspection shows valid manifests, not broken ones.
	broken := filepath.Join(dir, "broken.xml")
	if err := os.WriteFile(broken, []byte(
		`<file name="a.bin" chunks="2" total_size="10" md5="00000000000000000000000000000000">`+
			`<chunk id="0" from="0" to="9" method="md5">00000000000000000000000000000000</chunk>`+
			`</file>`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runManifestCLI(t, "manifest", "inspect", broken, "--json")
	if code != 2 {
		t.Fatalf("semantic error: exit %d, want 2 (out=%q)", code, out)
	}
	var errDoc map[string]any
	if err := json.Unmarshal([]byte(out), &errDoc); err != nil {
		t.Fatalf("semantic error --json not JSON: %s", out)
	}
	if errDoc["error_kind"] != "MANIFEST_SEMANTIC_ERROR" {
		t.Errorf("semantic error kind = %v, want MANIFEST_SEMANTIC_ERROR", errDoc["error_kind"])
	}
}
