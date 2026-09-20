package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// verifyRoot is the installation root the shared renderer tests report on.
const verifyRoot = "/games/W3 GOTY"

// verifyFact is one reported file under root: the path plus the state it is in.
// An error stands for a file whose state could not be observed at all.
func verifyFact(root, path string, status reconcile.FileStatus) core.FileFact {
	return core.FileFact{Destination: root + "/" + path, Status: status}
}

func renderVerifyResult(t *testing.T, res core.VerifyResult) (outcome, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	got := renderVerify(&out, &errOut, res)
	return got, out.String(), errOut.String()
}

// TestRenderVerifyHealthyTree locks the aggregate half of the output
// architecture: a verification of a tree that is exactly
// right is one summary line, and the routine state is a count rather than N
// per-file lines.
func TestRenderVerifyHealthyTree(t *testing.T) {
	res := core.VerifyResult{
		InstallPath: "/games/W3 GOTY",
		Facts: []core.FileFact{
			verifyFact(verifyRoot, "a.bin", reconcile.StatusOK),
			verifyFact(verifyRoot, "b.bin", reconcile.StatusOK),
		},
	}
	got, out, errOut := renderVerifyResult(t, res)

	if want := "Verifying → /games/W3 GOTY\n2 files: 2 OK\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
	if got != outcomeOK {
		t.Errorf("outcome = %v, want success", got)
	}
}

// TestRenderVerifyReportsEveryAbnormalFile locks the other half: only the files
// that need attention get a line, each with its code and its ABSOLUTE path — a
// mismatch is a diagnostic a user has to act on with the filesystem — and the
// summary counts every fact, so no file is silently unaccounted for.
func TestRenderVerifyReportsEveryAbnormalFile(t *testing.T) {
	unreadable := verifyFact(verifyRoot, "e.bin", reconcile.StatusUnset)
	unreadable.Err = errors.New("/games/W3 GOTY/e.bin: access is denied")

	res := core.VerifyResult{
		InstallPath: "/games/W3 GOTY",
		Facts: []core.FileFact{
			verifyFact(verifyRoot, "a.bin", reconcile.StatusOK),
			verifyFact(verifyRoot, "b.bin", reconcile.StatusND),
			verifyFact(verifyRoot, "c.bin", reconcile.StatusMD5),
			verifyFact(verifyRoot, "d.bin", reconcile.StatusFS),
			unreadable,
		},
	}
	got, out, errOut := renderVerifyResult(t, res)

	wantOut := strings.Join([]string{
		"Verifying → /games/W3 GOTY",
		"ND   /games/W3 GOTY/b.bin",
		"MD5  /games/W3 GOTY/c.bin",
		"FS   /games/W3 GOTY/d.bin",
		"5 files: 1 OK, 1 not downloaded, 1 md5 mismatch, 1 size mismatch, 1 unreadable",
		"",
	}, "\n")
	if out != wantOut {
		t.Errorf("stdout =\n%q\nwant\n%q", out, wantOut)
	}
	// The unreadable file is an error, so it goes to the error stream — and it
	// is still counted, which is what keeps the total honest.
	if want := "/games/W3 GOTY/e.bin: access is denied\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	if got != outcomeOperationFailure {
		t.Errorf("outcome = %v, want the operation failure code", got)
	}
}

// TestRenderVerifyNothingToVerify locks the empty case: a plan that expects no
// files says so instead of printing a bare zero, and the missing root keeps the
// header away.
func TestRenderVerifyNothingToVerify(t *testing.T) {
	got, out, errOut := renderVerifyResult(t, core.VerifyResult{})

	if want := "Nothing to verify.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
	if got != outcomeOK {
		t.Errorf("outcome = %v, want success: there was nothing to find", got)
	}
}

// TestRenderVerifyUnclassifiedFactIsNotHealthy locks the defensive rule the
// status zero value exists for: a fact with no status and no error is not a
// healthy file, so it takes a line and the run reports a failure — it cannot
// pass for OK.
func TestRenderVerifyUnclassifiedFactIsNotHealthy(t *testing.T) {
	res := core.VerifyResult{
		InstallPath: verifyRoot,
		Facts:       []core.FileFact{verifyFact(verifyRoot, "a.bin", reconcile.StatusOK), verifyFact(verifyRoot, "b.bin", reconcile.StatusUnset)},
	}
	got, out, errOut := renderVerifyResult(t, res)

	if !strings.Contains(out, "UNSET  /games/W3 GOTY/b.bin") {
		t.Errorf("stdout = %q, want the unclassified file named", out)
	}
	if !strings.Contains(out, "2 files: 1 OK, 1 unreadable") {
		t.Errorf("stdout = %q, want it counted as needing attention", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing: this is not an observation failure", errOut)
	}
	if got != outcomeOperationFailure {
		t.Errorf("outcome = %v, want the operation failure code", got)
	}
}

// TestVerifyHelpDocumentsTheCodes locks the topic a user reads before trusting
// the report: the four codes are defined there and the read-only promise is
// stated.
func TestVerifyHelpDocumentsTheCodes(t *testing.T) {
	code, out, _ := run(t, "", "verify", "-h")
	if code != 0 {
		t.Fatalf("verify -h exit = %d, want 0", code)
	}
	for _, want := range []string{"ND (not downloaded)", "MD5 (content differs)", "FS (size differs)", "never fixed"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify -h is missing %q: %q", want, out)
		}
	}
}
