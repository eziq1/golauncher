// Package store is the host's SQLite database: plugin key/value storage,
// usage statistics for ranking (frecency) and recently used items.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// DB wraps the host database.
type DB struct {
	db *sql.DB
}

// migrations[v] upgrades the schema from version v-1 to v; the version is
// kept in PRAGMA user_version. Append new migrations, never edit released
// ones. Statements must be idempotent (IF NOT EXISTS, …) so that databases
// created before versioning (user_version 0, tables present) upgrade cleanly.
var migrations = []string{
	0: ``,
	1: `
CREATE TABLE IF NOT EXISTS kv (
	ns    TEXT NOT NULL,
	key   TEXT NOT NULL,
	value BLOB NOT NULL,
	PRIMARY KEY (ns, key)
);
CREATE TABLE IF NOT EXISTS usage (
	item_id   TEXT PRIMARY KEY,
	score     REAL NOT NULL,    -- decayed use count as of last_used
	last_used INTEGER NOT NULL, -- unix milliseconds
	item      BLOB NOT NULL     -- JSON of the entry, for the recent list
);
`,
}

// SchemaVersion is the schema version this build creates and understands.
var SchemaVersion = len(migrations) - 1

// ErrNewerSchema is returned by Open for a database written by a newer
// version of golauncher.
var ErrNewerSchema = errors.New("store: database was created by a newer version of golauncher")

// migrate upgrades db to the latest schema, one transaction per version.
func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > SchemaVersion {
		return fmt.Errorf("%w (version %d, supported %d)", ErrNewerSchema, v, SchemaVersion)
	}
	for next := v + 1; next <= SchemaVersion; next++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[next]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migrate to version %d: %w", next, err)
		}
		// PRAGMA does not take bound parameters; next is an int.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, next)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Open opens (creating if needed) the database at path. ":memory:" gives a
// private in-memory database.
func Open(path string) (*DB, error) {
	dsn := fileURI(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	if path == ":memory:" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Every connection would get its own empty in-memory database.
		db.SetMaxOpenConns(1)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// fileURI returns a SQLite file: URI for path with special characters such
// as '#' and '%' escaped (they are legal in Windows paths and user names).
func fileURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// KV returns the key/value store of namespace ns.
func (d *DB) KV(ns string) plugin.KV { return kv{d.db, ns} }

type kv struct {
	db *sql.DB
	ns string
}

func (s kv) Get(key string) ([]byte, bool, error) {
	var v []byte
	err := s.db.QueryRow(`SELECT value FROM kv WHERE ns = ? AND key = ?`, s.ns, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return v, err == nil, err
}

func (s kv) Set(key string, value []byte) error {
	_, err := s.db.Exec(`INSERT INTO kv (ns, key, value) VALUES (?, ?, ?)
		ON CONFLICT (ns, key) DO UPDATE SET value = excluded.value`, s.ns, key, value)
	return err
}

func (s kv) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM kv WHERE ns = ? AND key = ?`, s.ns, key)
	return err
}

// HalfLife is how long it takes a use to count half as much.
const HalfLife = 7 * 24 * time.Hour

func decay(score float64, from, to time.Time) float64 {
	dt := to.Sub(from)
	if dt <= 0 {
		return score
	}
	return score * math.Exp2(-float64(dt)/float64(HalfLife))
}

// RecordUse counts one use of an item at time now. item is the item's JSON
// so it can be shown in the recent list later.
func (d *DB) RecordUse(itemID string, item []byte, now time.Time) error {
	var score float64
	var last int64
	err := d.db.QueryRow(`SELECT score, last_used FROM usage WHERE item_id = ?`, itemID).Scan(&score, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	score = decay(score, time.UnixMilli(last), now) + 1
	_, err = d.db.Exec(`INSERT INTO usage (item_id, score, last_used, item) VALUES (?, ?, ?, ?)
		ON CONFLICT (item_id) DO UPDATE SET score = excluded.score, last_used = excluded.last_used, item = excluded.item`,
		itemID, score, now.UnixMilli(), item)
	return err
}

// Frecency returns the decayed use counts of all items as of now.
func (d *DB) Frecency(now time.Time) (map[string]float64, error) {
	rows, err := d.db.Query(`SELECT item_id, score, last_used FROM usage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var score float64
		var last int64
		if err := rows.Scan(&id, &score, &last); err != nil {
			return nil, err
		}
		out[id] = decay(score, time.UnixMilli(last), now)
	}
	return out, rows.Err()
}

// Recent returns the JSON of the n most recently used items, newest first.
func (d *DB) Recent(n int) ([][]byte, error) {
	rows, err := d.db.Query(`SELECT item FROM usage ORDER BY last_used DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ForgetUse removes an item from the usage statistics.
func (d *DB) ForgetUse(itemID string) error {
	_, err := d.db.Exec(`DELETE FROM usage WHERE item_id = ?`, itemID)
	return err
}

// ClearUsage removes all usage statistics.
func (d *DB) ClearUsage() error {
	_, err := d.db.Exec(`DELETE FROM usage`)
	return err
}
