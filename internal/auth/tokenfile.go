package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// tokenFileMode mirrors Util::setFilePermissions(owner_read|owner_write)
// (downloader.cpp:3807,171). 0600 is Unix semantics; on Windows no claim of
// equivalent ACL behaviour is made (review lock, D4).
const tokenFileMode = 0o600

// SaveTokenFile persists g's token store to path as compact JSON. The output
// is JSON-semantically equivalent to the C++ ofs << Json::Value write
// (downloader.cpp:3792-3807), not byte-identical (StyledWriter vs
// json.Marshal). An empty store writes nothing and returns nil, mirroring
// saveGalaxyJSON's guard on empty JSON.
//
// The write is atomic (S10a review lock): content goes to a temp file in the
// same directory that is created with mode 0600 from the start, synced, and
// renamed over path — a crash never leaves a truncated token file, and the
// secret never exists with looser permissions before an explicit chmod.
func SaveTokenFile(g *config.GalaxyConfig, path string) error {
	store := g.GetJSON()
	if len(store) == 0 {
		return nil
	}
	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("auth: marshal token store: %w", err)
	}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	return nil
}

// writeAtomic writes data to path via a 0600 temp file plus rename.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".goggo-token-*")
	if err != nil {
		return fmt.Errorf("auth: create temp token file in %q: %w", dir, err)
	}
	// Enforce 0600 immediately after creation, before any content is
	// written, so the secret never sits at looser permissions.
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

// LoadTokenFile reads the token store at path into g, mirroring the loader
// in Downloader::Downloader (downloader.cpp:140-150). When the file lacks
// expires_at, it is derived from the FILE MODIFICATION TIME plus expires_in
// (review lock: never the current time), preserving the original lifetime of
// a token file written earlier. C++ semantics kept: a missing expires_in
// contributes 0, so expires_at lands on the file mtime and the token reads
// as already expired.
func LoadTokenFile(g *config.GalaxyConfig, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("auth: stat token file %q: %w", path, err)
	}
	v, err := util.ReadJSONFile(path)
	if err != nil {
		return err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("auth: token file %q: not a JSON object", path)
	}
	if _, has := obj["expires_at"]; !has {
		obj["expires_at"] = fi.ModTime().Unix() + jsonInt64(obj["expires_in"])
	}
	g.SetJSON(obj)
	return nil
}

// jsonInt64 mirrors the numeric reader used inside config for token values.
// It tolerates the types encoding/json produces for numbers (float64,
// json.Number) plus native integers; anything else yields 0.
func jsonInt64(v any) int64 {
	switch n := v.(type) {
	case nil:
		return 0
	case float64:
		return int64(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return i
	case int:
		return int64(n)
	case int64:
		return n
	case uint64:
		return int64(n)
	default:
		return 0
	}
}
