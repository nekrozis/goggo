package auth

import (
	"encoding/hex"
	"testing"
)

// goldenCredentials is the exact encoding of goldenTree, captured from the
// implementation as it stood before the framing moved into a shared package.
//
// It is deliberately a captured value rather than one produced by the code under
// test: the point of the move was to share the framing without changing what is
// already on disk, and only a fixed byte string can show that. The tree below is
// fixed — no clock, no derived expiry, no map ordering — so the expected value
// cannot drift with the environment.
const goldenCredentials = "474f47474f4155544801000000811c4d06040c4810013a100a05110743560f120042140c" +
	"551355454345040344061c113b0c0a5653430f441759091b1d4812134b4d040b06480d063a17000d060c" +
	"154e1751171c5f03440e54034d4b450a55131b17011631151d43561c44445f42551d4601574345150a4b" +
	"1117160c3a1a1b0204020f49561d06484b1f49020b451a810cf593"

// goldenTree is the fixed token tree the golden bytes encode.
func goldenTree() map[string]any {
	return map[string]any{
		"access_token":  "at-fixed",
		"refresh_token": "rt-fixed",
		"expires_at":    float64(1700000000),
		"client_id":     "cid-fixed",
		"client_secret": "cs-fixed",
	}
}

// TestCredentialsFormatIsUnchanged locks the on-disk format of credentials.bin
// against the refactor that moved its framing into secretfile. A session written
// by the previous build must keep decoding, and a session written now must be
// the bytes the previous build would have written.
func TestCredentialsFormatIsUnchanged(t *testing.T) {
	got, err := encodeStore(goldenTree())
	if err != nil {
		t.Fatalf("encodeStore: %v", err)
	}
	if hex.EncodeToString(got) != goldenCredentials {
		t.Errorf("the encoding changed:\n got %s\nwant %s", hex.EncodeToString(got), goldenCredentials)
	}

	// The reverse direction matters just as much: a file already on disk has to
	// keep opening.
	raw, err := hex.DecodeString(goldenCredentials)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeStore(raw)
	if err != nil {
		t.Fatalf("decodeStore of the captured bytes: %v", err)
	}
	if back["access_token"] != "at-fixed" || back["client_secret"] != "cs-fixed" {
		t.Errorf("decoded tree = %v, want the captured one", back)
	}
}
