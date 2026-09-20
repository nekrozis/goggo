package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/secretfile"
)

// defaultExpiresIn is the token lifetime assumed when the server response
// carries no usable expires_in.
const defaultExpiresIn int64 = 3600

// Store is the credential seam: it owns the Galaxy session's token tree and the
// operations that act on it. Nothing outside this file reads the tree, and no
// exported method hands out a credential — a caller can obtain the value it
// needs for one purpose (an Authorization header, an expiry answer) and nothing
// else.
//
// The rule the surface encodes: a purpose-specific operation is fine, a generic
// raw-secret accessor is not. There is deliberately no AccessToken(), no
// GetJSON() and no String() or Format(), because each of those is a way for a
// credential to reach a message, a log or a struct field without anyone
// deciding that it should.
//
// Use it through a pointer: sharing the pointer IS the design, and a copy would
// duplicate the lock and the state.
type Store struct {
	mu       sync.RWMutex
	path     string
	token    map[string]any
	redirect string
}

// StorePath is where the session's token store lives. It is defined here
// because the seam owns the location as well as the contents.
//
// The name is deliberately not the one an earlier build used: a file called
// credentials.bin cannot be mistaken for the JSON token file this program wrote
// before, and that file is never read, migrated, overwritten or deleted.
func StorePath(cfg config.Config) string {
	return cfg.ConfigDirectory + "/credentials.bin"
}

// Open returns the store at path: an empty one when no file exists yet (a fresh
// install is normal, not an error), an error when a file exists but cannot be
// read or does not decode.
//
// An empty path is also an empty store, and no error: a store may exist before a
// location is chosen for it, and Save already refuses to persist one without a
// path, so nothing can be written by accident.
//
// A store that lacks expires_at has it derived from the file's modification time
// plus expires_in, which preserves the lifetime of a file written earlier.
func Open(path string) (*Store, error) {
	s := &Store{path: path, token: map[string]any{}, redirect: config.DefaultRedirectURI}
	if path == "" {
		return s, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("auth: stat store file %q: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read store file %q: %w", path, err)
	}
	obj, err := decodeStore(data)
	if err != nil {
		return nil, fmt.Errorf("auth: store file %q: %w", path, err)
	}
	if _, has := obj["expires_at"]; !has {
		obj["expires_at"] = fi.ModTime().Unix() + jsonInt64Value(obj["expires_in"])
	}
	s.token = cloneJSONMap(obj)
	return s, nil
}

// RemoveStore deletes the token store at path, treating "already gone" as
// success: logout clears state that may not be there, and must not fail because
// of it.
//
// This is a path-level operation rather than a Store method on purpose. A logout
// must not first parse the file it is about to delete — a corrupt token file
// would then make clearing it impossible.
func RemoveStore(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("auth: remove token file %q: %w", path, err)
}

// AuthorizationValue returns the value for an Authorization header ("Bearer
// …"), or "" when there is no usable token.
//
// The token is returned in the form a request needs it, never on its own: the
// caller has a reason to send it and no way to store it. An expired store
// contributes nothing, so the request goes out unauthenticated and the server's
// answer decides the outcome.
func (s *Store) AuthorizationValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.expiredAtLocked(time.Now().Unix()) {
		return ""
	}
	tok, _ := jsonString(s.token, "access_token")
	if tok == "" {
		return ""
	}
	return "Bearer " + tok
}

// Expired reports whether the access token has passed its expires_at. A token
// without a usable expires_at counts as expired.
func (s *Store) Expired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiredAtLocked(time.Now().Unix())
}

// expiredAtLocked is Expired with an injected clock. Callers hold the lock.
func (s *Store) expiredAtLocked(now int64) bool {
	exp, ok := jsonInt64(s.token, "expires_at")
	if !ok {
		return true
	}
	return now > exp
}

// Empty reports whether the store holds no token at all.
func (s *Store) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.token) == 0
}

// ClientID returns the stored client_id override, falling back to the Galaxy
// protocol default.
//
// This is a PROTOCOL credential, not a user one: it identifies goggo's Galaxy
// client to the OAuth server, is the same for every user and is not derived from
// anyone's account. It is therefore part of the ordinary surface, unlike the
// access and refresh tokens.
func (s *Store) ClientID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := jsonString(s.token, "client_id"); ok {
		return v
	}
	return config.DefaultClientID
}

// ClientSecret returns the stored client_secret override, falling back to the
// Galaxy protocol default. See ClientID for why it is not treated as a user
// credential.
func (s *Store) ClientSecret() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := jsonString(s.token, "client_secret"); ok {
		return v
	}
	return config.DefaultClientSecret
}

// RedirectURI returns the OAuth redirect URI.
func (s *Store) RedirectURI() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.redirect
}

// ResetClient restores the default client_id/client_secret when the store
// carries overrides; absent keys stay absent, since the getters already fall
// back to the defaults.
func (s *Store) ResetClient() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.token["client_id"]; ok {
		s.token["client_id"] = config.DefaultClientID
	}
	if _, ok := s.token["client_secret"]; ok {
		s.token["client_secret"] = config.DefaultClientSecret
	}
}

// StoreLoginResponse stores a login or refresh response. It is the seam's only
// write path from outside.
//
// The map is deep-copied and never retained: the caller keeps its own value and
// cannot reach into the store by holding onto it. When the response lacks
// expires_at it is derived from expires_in (default 3600), so expiry can be
// evaluated later without a clock dependency.
func (s *Store) StoreLoginResponse(token map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := cloneJSONMap(token)
	if _, ok := stored["expires_at"]; !ok {
		expiresIn := defaultExpiresIn
		if v, ok := jsonInt64(stored, "expires_in"); ok {
			expiresIn = v
		}
		stored["expires_at"] = time.Now().Unix() + expiresIn
	}
	s.token = stored
}

// Refresh renews the access token through c and stores the result. It does NOT
// persist: the caller decides when to call Save, because a refresh that succeeds
// in memory and then fails to write is a different outcome from one that never
// happened.
func (s *Store) Refresh(ctx context.Context, c *Client) error {
	return c.refresh(ctx, s, true)
}

// Save persists the store, atomically and with 0600 from creation. An empty
// store writes nothing: a file holding no credential is a file that can only
// confuse the next read.
func (s *Store) Save() error {
	s.mu.RLock()
	store := cloneJSONMap(s.token)
	path := s.path
	s.mu.RUnlock()

	if len(store) == 0 {
		return nil
	}
	if path == "" {
		return errors.New("auth: no token file path set")
	}
	data, err := encodeStore(store)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// The on-disk envelope. It is a NON-PLAINTEXT, NON-ENCRYPTED format: the
// payload is obfuscated with a fixed keystream that anyone holding this source
// can recover. It provides no cryptographic confidentiality and is not meant to
// resist malware, EDR or anyone analysing the machine. Its only effect is that
// the file is no longer text, so a plain grep or a text index does not pick up
// the credential fields.
//
// The framing itself lives in secretfile, which knows nothing about tokens; this
// file supplies the magic, the version and the key, so the format is this
// package's choice even though the layout is shared.
//
//	crc32 covers truncation, random corruption and a partial write. It is an
//	integrity check, not a security measure: rewriting the payload lets an
//	attacker rewrite the checksum too.
const (
	storeMagic   = "GOGGOAUTH"
	storeVersion = 1
)

// storeObfuscation is the key the token payload is XORed with. It is
// obfuscation material, not a secret, and it is deliberately NOT derived from
// the magic: the bytes this program has already written must keep decoding, and
// a later change to another file kind's framing must not reach this one.
var storeObfuscation = secretfile.XOR("goggo-credential-store-v1")

// errStorePayload is the one failure the framing cannot report, because it is
// about what the payload means rather than how it is framed.
var errStorePayload = errors.New("store payload is not a JSON object")

// encodeStore serialises a token tree into the on-disk envelope.
func encodeStore(store map[string]any) ([]byte, error) {
	plain, err := json.Marshal(store)
	if err != nil {
		return nil, fmt.Errorf("auth: marshal token store: %w", err)
	}
	return secretfile.Encode(storeMagic, storeVersion, storeObfuscation, plain), nil
}

// decodeStore parses the envelope and then the token tree inside it. A framing
// failure is reported by secretfile as itself; a payload that decodes but is not
// a JSON object is this package's failure.
func decodeStore(data []byte) (map[string]any, error) {
	plain, err := secretfile.Decode(storeMagic, storeVersion, storeObfuscation, data)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(plain, &obj); err != nil || obj == nil {
		return nil, errStorePayload
	}
	return obj, nil
}

// tokenFileMode is the permission mode for token files. 0600 is Unix semantics;
// on Windows no equivalent ACL behaviour is claimed.
const tokenFileMode = 0o600

// writeAtomic writes data to path via a 0600 temp file plus rename: a crash never
// leaves a truncated token file, and the secret never exists with looser
// permissions.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".goggo-token-*")
	if err != nil {
		return fmt.Errorf("auth: create temp token file in %q: %w", dir, err)
	}
	// Enforce 0600 immediately after creation, before any content is written,
	// so the secret never sits at looser permissions.
	if err := tmp.Chmod(tokenFileMode); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("auth: chmod temp token file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("auth: write temp token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("auth: sync temp token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("auth: close temp token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("auth: replace token file %q: %w", path, err)
	}
	return nil
}

// refreshToken and accessToken are the seam's INTERNAL reads: they exist so
// Refresh can build its request without any exported getter existing. They are
// unexported and stay that way — a caller outside this package has no
// legitimate use for either value on its own.
func (s *Store) refreshToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, _ := jsonString(s.token, "refresh_token")
	return v
}

// jsonString reads a string field; ok is false when the key is absent or not a
// JSON string.
func jsonString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// jsonInt64 reads an integer field, tolerating the numeric types encoding/json
// produces (float64, json.Number) plus native integers. Numbers that cannot be
// represented as int64 are reported as absent rather than converted: Go's
// float→int conversion is implementation-defined for them, and a malformed
// expires_in/expires_at must not fabricate a value.
func jsonInt64(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if n > float64(1<<63-1) || n < float64(-1<<63) {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		if n > 1<<63-1 {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}

// jsonInt64Value reads a bare JSON value as int64, reporting 0 when it is not a
// usable number. It is the value-shaped form of jsonInt64, for the one caller
// that holds a value rather than a map and key.
func jsonInt64Value(v any) int64 {
	n, _ := jsonInt64(map[string]any{"v": v}, "v")
	return n
}

// cloneJSONMap deep-copies a token map. Only the shapes encoding/json produces
// are copied; anything else is dropped, because the store is a JSON tree and a
// caller must not be able to alias it.
func cloneJSONMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneJSONValue(v)
	}
	return out
}

// cloneJSONValue is the recursive core of cloneJSONMap.
func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneJSONMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneJSONValue(e)
		}
		return out
	default:
		return v
	}
}
