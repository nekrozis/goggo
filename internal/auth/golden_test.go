package auth

import (
	"bytes"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nekrozis/goggo/internal/secretfile"
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

// TestCredentialsWireFormatCompatibility locks the legacy JSON wire representation
// of token tree fields against unintended v2 serializer defaults.
//
// HTML characters (<>&) must be escaped, JavaScript line/paragraph separators
// (U+2028, U+2029) must be escaped, and nil maps/slices must be formatted as null
// to preserve on-disk wire compatibility with credentials written by earlier builds.
func TestCredentialsWireFormatCompatibility(t *testing.T) {
	tree := map[string]any{
		"a_html":      "<script>&foo</script>",
		"b_js":        "line1\u2028line2\u2029line3",
		"c_nil_map":   (map[string]int)(nil),
		"d_nil_slice": ([]string)(nil),
		"e_number":    float64(42),
	}

	// Contract (format): exact wire bytes for token trees
	const want = `{"a_html":"\u003cscript\u003e\u0026foo\u003c/script\u003e","b_js":"line1\u2028line2\u2029line3","c_nil_map":null,"d_nil_slice":null,"e_number":42}`

	enc, err := encodeStore(tree)
	if err != nil {
		t.Fatalf("encodeStore: %v", err)
	}
	plain, err := secretfile.Decode(storeMagic, storeVersion, storeObfuscation, enc)
	if err != nil {
		t.Fatalf("secretfile.Decode: %v", err)
	}
	if string(plain) != want {
		t.Fatalf("wire bytes mismatch:\n got  %s\n want %s", string(plain), want)
	}

	// Round-trip verification: the decoded store preserves original values.
	back, err := decodeStore(enc)
	if err != nil {
		t.Fatalf("decodeStore: %v", err)
	}
	if back["a_html"] != "<script>&foo</script>" {
		t.Errorf("a_html = %v, want <script>&foo</script>", back["a_html"])
	}
	if back["b_js"] != "line1\u2028line2\u2029line3" {
		t.Errorf("b_js = %v, want line1\\u2028line2\\u2029line3", back["b_js"])
	}
	if back["c_nil_map"] != nil {
		t.Errorf("c_nil_map = %v, want nil", back["c_nil_map"])
	}
	if back["d_nil_slice"] != nil {
		t.Errorf("d_nil_slice = %v, want nil", back["d_nil_slice"])
	}
	if back["e_number"] != float64(42) {
		t.Errorf("e_number = %v, want 42", back["e_number"])
	}
}

// TestCredentialsEncodingIsDeterministic verifies that repeated serialisations
// of a token tree produce identical wire bytes, locking deterministic key ordering.
func TestCredentialsEncodingIsDeterministic(t *testing.T) {
	tree := map[string]any{
		"z_token": "token-z",
		"a_token": "token-a",
		"m_token": "token-m",
		"b_token": "token-b",
		"k_token": "token-k",
	}
	var baseline []byte
	for i := range 20 {
		enc, err := encodeStore(tree)
		if err != nil {
			t.Fatalf("iteration %d: encodeStore: %v", i, err)
		}
		if i == 0 {
			baseline = enc
			continue
		}
		if !bytes.Equal(enc, baseline) {
			t.Fatalf("iteration %d produced divergent wire bytes; encoding must be deterministic", i)
		}
	}
}

// TestEncodeStoreDeclaresDeterministicOption is an AST source guard verifying that
// encodeStore in store.go explicitly passes jsonv2.Deterministic(true) to jsonv2.Marshal,
// eliminating any probabilistic gap in mutation verification.
func TestEncodeStoreDeclaresDeterministicOption(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	storePath := filepath.Join(filepath.Dir(filename), "store.go")

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, storePath, nil, 0)
	if err != nil {
		t.Fatalf("parse store.go: %v", err)
	}

	foundOption := false
	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "encodeStore" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Marshal" {
				return true
			}
			for _, arg := range call.Args {
				argCall, ok := arg.(*ast.CallExpr)
				if !ok {
					continue
				}
				argSel, ok := argCall.Fun.(*ast.SelectorExpr)
				if !ok || argSel.Sel.Name != "Deterministic" {
					continue
				}
				if len(argCall.Args) == 1 {
					if ident, ok := argCall.Args[0].(*ast.Ident); ok && ident.Name == "true" {
						foundOption = true
					}
				}
			}
			return true
		})
	}
	if !foundOption {
		t.Fatal("encodeStore must explicitly declare jsonv2.Deterministic(true)")
	}
}
