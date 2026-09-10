package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/nekrozis/goggo/internal/config"
)

// logout clears the local login state: the Galaxy token store and the cookie
// jar.
//
// This is a goggo extension. Upstream lgogdownloader has no logout option and
// no remote logout API, so nothing here talks to GOG — it removes local files
// and nothing else.
//
// The scope is exactly the two authentication files. They sit in the
// configuration directory next to files that are NOT authentication state
// (config.cfg, blacklist.txt, ignorelist.txt, transformations.json), and the
// cache root holds the XML directory; all of those are left alone, as is any
// other program's data.
//
// It deliberately does NOT go through Open: a Session flushes its cookie jar on
// Close, which would write cookies.txt straight back and undo the removal, and
// an Open that is allowed to log in could start a fresh login. Nothing here
// creates a directory, opens a socket or reads a cookie.
//
// Removal is idempotent: a path that is already gone counts as success, so
// running --logout twice is as successful as running it once. Only a genuine
// removal failure returns an error, naming the path it could not remove; the
// success line is then not printed, so the output never claims more than
// happened. The removal is not transactional — a failure on the second path
// leaves the first one already gone.
//
// The paths come from the configuration rather than being rebuilt here, so a
// later configurable cookie path is followed automatically.
func logout(cfg config.Config, out io.Writer) error {
	for _, path := range []string{tokenPath(cfg), cfg.Curl.CookiePath} {
		if err := removeAuthFile(path); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "Local login state cleared")
	return nil
}

// removeAuthFile removes one authentication file, treating "already gone" as
// success.
//
// os.Remove is used as it is: what it removes is whatever the path names, and
// this function does not first require the target to be a regular file. A
// symlink would be removed rather than followed, an empty directory would be
// removed, and a non-empty directory or a locked file is normally refused — but
// that refusal is the operating system's, not a rule stated here, and an
// environment that redirects deletions may report success instead. The two
// paths are what make the target authentication state; the type check is not.
func removeAuthFile(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove %s: %w", path, err)
}
