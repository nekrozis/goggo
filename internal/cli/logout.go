package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
)

// logout clears the local login state: the Galaxy token store and the cookie
// jar, and nothing else — the configuration directory's other files and the
// cache root are left alone.
//
// It deliberately does NOT go through core.Open: a core.Downloader flushes its
// cookie jar on Close, which would write cookies.txt straight back and undo the
// removal, and an Open that is allowed to log in could start a fresh login.
// Nothing here creates a directory, opens a socket or reads a cookie.
//
// Removal is idempotent — an already-gone path counts as success, so only a
// genuine removal failure returns an error, naming the path; the success line is
// then not printed, and a failure on the second path leaves the first gone.
func logout(cfg config.Config, out io.Writer) error {
	for _, path := range []string{core.TokenPath(cfg), cfg.Curl.CookiePath} {
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
// os.Remove is used as it is, without first requiring a regular file: a symlink
// is removed rather than followed and an empty directory is removed. Refusing a
// non-empty directory or a locked file is the operating system's rule, not one
// stated here.
func removeAuthFile(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove %s: %w", path, err)
}
