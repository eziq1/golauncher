package winget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FixtureOptions configures the plugin for a fake environment whose
// winget is described by files in root/winget (glctl --fixtures):
//
//   - index.sql: SQL that builds the index of the "winget" source (the
//     schema 2.0 tables of the real index), loaded into an in-memory SQLite
//     database, so the real reading code runs;
//   - pinning.sql: SQL that builds pinning.db (optional);
//   - sources.jsonl: the output of `winget source export`;
//   - list.txt: the output of `winget list`;
//   - show/<id>.txt: the output of `winget show --id <id>`.
//
// Without root/winget, winget is not installed and the plugin is
// unavailable. Changes are background tasks, which the fake environment's
// task runner only records.
func FixtureOptions(root string) Options {
	o := Options{NoDetect: true, Sync: true, Getenv: func(string) string { return "" }}
	if root == "" {
		return o
	}
	dir := filepath.Join(root, "winget")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return o
	}
	o.Exe = "winget.exe"
	o.Run = FixtureRunner(dir)
	o.IndexStamp = func(Source) (string, error) { return "fixture", nil }
	o.OpenIndex = func(Source) (*sql.DB, error) { return openSQLFixture(filepath.Join(dir, "index.sql")) }
	o.OpenPins = func() (*sql.DB, error) { return openSQLFixture(filepath.Join(dir, "pinning.sql")) }
	return o
}

// openSQLFixture returns an in-memory database built by the SQL file path.
func openSQLFixture(path string) (*sql.DB, error) {
	script, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one connection: the database lives in it
	if _, err := db.Exec(string(script)); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return db, nil
}

// FixtureRunner answers read-only winget commands from the files in dir
// (see FixtureOptions). Other commands fail.
func FixtureRunner(dir string) Runner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var file string
		switch {
		case len(args) >= 2 && args[0] == "source" && args[1] == "export":
			file = "sources.jsonl"
		case len(args) >= 1 && args[0] == "list":
			file = "list.txt"
		case len(args) >= 1 && args[0] == "show":
			i := slices.Index(args, "--id")
			if i < 0 || i+1 >= len(args) || !idRe.MatchString(args[i+1]) {
				return nil, errors.New("winget fixture: show needs --id")
			}
			file = filepath.Join("show", strings.ToLower(args[i+1])+".txt")
		default:
			return nil, fmt.Errorf("winget fixture: no answer for %q", args)
		}
		b, err := os.ReadFile(filepath.Join(dir, file))
		if errors.Is(err, fs.ErrNotExist) && args[0] == "show" {
			msg := "No package found matching input criteria.\n"
			return []byte(msg), &ExitError{Code: -1978335212, Output: msg}
		}
		return b, err
	}
}
