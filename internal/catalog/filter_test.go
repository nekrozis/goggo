package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFilterList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filters.txt")
	if err := os.WriteFile(path, []byte("alpha\n\nbeta\r\ngamma"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := LoadFilterList(path)
	if err != nil {
		t.Fatalf("LoadFilterList: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"} // blank lines dropped, CR stripped
	if len(lines) != len(want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestLoadFilterListMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := LoadFilterList(path)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q must carry the path", err)
	}
}

func TestCompileFilters(t *testing.T) {
	t.Run("game regex wins over list path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "filters.txt")
		if err := os.WriteFile(path, []byte("beta"), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := CompileFilters("alpha", path, "")
		if err != nil {
			t.Fatalf("CompileFilters: %v", err)
		}
		if len(f.Games) != 1 || !f.Games[0].MatchString("alpha") {
			t.Errorf("games = %v, want only the --game-regex", f.Games)
		}
		if f.IgnoreDLCCount != nil {
			t.Error("IgnoreDLCCount should be nil")
		}
	})

	t.Run("invalid game regex", func(t *testing.T) {
		if _, err := CompileFilters("(", "", ""); err == nil {
			t.Error("want error")
		}
	})

	t.Run("invalid ignore regex", func(t *testing.T) {
		if _, err := CompileFilters("", "", "("); err == nil {
			t.Error("want error")
		}
	})

	t.Run("invalid list line reports its number", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "filters.txt")
		if err := os.WriteFile(path, []byte("ok\n(\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := CompileFilters("", path, "")
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("error %q should name the offending line", err)
		}
	})

	t.Run("no configuration", func(t *testing.T) {
		f, err := CompileFilters("", "", "")
		if err != nil {
			t.Fatalf("CompileFilters: %v", err)
		}
		if len(f.Games) != 0 || f.IgnoreDLCCount != nil {
			t.Errorf("filters = %+v, want empty", f)
		}
	})
}

func TestMatchesAny(t *testing.T) {
	f, err := CompileFilters("alpha", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !MatchesAny(f.Games, "the-alpha-game") {
		t.Error("substring match expected")
	}
	if MatchesAny(f.Games, "beta") {
		t.Error("unexpected match")
	}
	if MatchesAny(nil, "anything") {
		t.Error("no filters must not match")
	}
}
