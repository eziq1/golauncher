package clipboard

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Clip kinds stored in clips.kind.
const (
	kindText  = "text"
	kindImage = "image"
	kindFiles = "files"
)

// clip is a row of the clips table.
type clip struct {
	ID         int64
	Kind       string
	Text       string
	Files      []string
	Image      string // file name under DataDir/images
	Width      int
	Height     int
	Size       int64 // bytes of text or PNG; number of files
	SourceApp  string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Pinned     bool
}

// migrations[v] upgrades the schema from version v-1 to v. Every statement
// is idempotent so a half-applied migration can be rerun.
//
// clips_fts uses the trigram tokenizer: it matches substrings, which works
// for languages without spaces between words (e.g. Chinese) and for paths.
var migrations = []string{
	1: `
CREATE TABLE IF NOT EXISTS clips (
	id           INTEGER PRIMARY KEY,
	kind         TEXT    NOT NULL,             -- text | image | files
	text         TEXT    NOT NULL DEFAULT '',
	files        TEXT    NOT NULL DEFAULT '',  -- JSON array of paths
	image        TEXT    NOT NULL DEFAULT '',  -- file name under images/
	width        INTEGER NOT NULL DEFAULT 0,
	height       INTEGER NOT NULL DEFAULT 0,
	hash         TEXT    NOT NULL UNIQUE,      -- SHA-256 of kind and content
	size         INTEGER NOT NULL DEFAULT 0,
	source_app   TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,             -- unix milliseconds
	last_used_at INTEGER NOT NULL,             -- unix milliseconds
	pinned       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS clips_order ON clips (pinned, last_used_at);
CREATE VIRTUAL TABLE IF NOT EXISTS clips_fts USING fts5(body, tokenize = 'trigram');
CREATE TRIGGER IF NOT EXISTS clips_delete AFTER DELETE ON clips BEGIN
	DELETE FROM clips_fts WHERE rowid = old.id;
END;
`,
}

// store is the clipboard history database. It is safe for concurrent use.
type store struct {
	db *sql.DB
}

func openStore(path string) (*store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("clipboard: migrate %s: %w", path, err)
	}
	return s, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if latest := len(migrations) - 1; v > latest {
		return fmt.Errorf("schema version %d is newer than supported %d", v, latest)
	}
	for next := v + 1; next < len(migrations); next++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[next]); err != nil {
			tx.Rollback()
			return fmt.Errorf("version %d: %w", next, err)
		}
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

// add inserts c, or, if a clip with the same hash exists, only updates its
// last use time. body is the text indexed for search.
func (s *store) add(c clip, hash, body string, now time.Time) (id int64, added bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	ms := now.UnixMilli()
	err = tx.QueryRow(`SELECT id FROM clips WHERE hash = ?`, hash).Scan(&id)
	switch {
	case err == nil:
		if _, err := tx.Exec(`UPDATE clips SET last_used_at = ? WHERE id = ?`, ms, id); err != nil {
			return 0, false, err
		}
		return id, false, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}
	var files string
	if len(c.Files) > 0 {
		b, err := json.Marshal(c.Files)
		if err != nil {
			return 0, false, err
		}
		files = string(b)
	}
	res, err := tx.Exec(`INSERT INTO clips (kind, text, files, image, width, height, hash, size, source_app, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Kind, c.Text, files, c.Image, c.Width, c.Height, hash, c.Size, c.SourceApp, ms, ms)
	if err != nil {
		return 0, false, err
	}
	if id, err = res.LastInsertId(); err != nil {
		return 0, false, err
	}
	if body != "" {
		if _, err := tx.Exec(`INSERT INTO clips_fts (rowid, body) VALUES (?, ?)`, id, body); err != nil {
			return 0, false, err
		}
	}
	return id, true, tx.Commit()
}

// deleteWhere deletes the clips matching cond and returns how many were
// deleted and their image file names.
func (s *store) deleteWhere(cond string, args ...any) (int, []string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT image FROM clips WHERE `+cond, args...)
	if err != nil {
		return 0, nil, err
	}
	var images []string
	for rows.Next() {
		var img string
		if err := rows.Scan(&img); err != nil {
			rows.Close()
			return 0, nil, err
		}
		if img != "" {
			images = append(images, img)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	res, err := tx.Exec(`DELETE FROM clips WHERE `+cond, args...)
	if err != nil {
		return 0, nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	return int(n), images, tx.Commit()
}

// prune deletes unpinned clips beyond the newest maxItems and those not used
// for maxDays. Zero disables a limit.
func (s *store) prune(now time.Time, maxItems, maxDays int) ([]string, error) {
	var images []string
	if maxDays > 0 {
		cutoff := now.Add(-time.Duration(maxDays) * 24 * time.Hour).UnixMilli()
		_, imgs, err := s.deleteWhere(`pinned = 0 AND last_used_at < ?`, cutoff)
		if err != nil {
			return nil, err
		}
		images = append(images, imgs...)
	}
	if maxItems > 0 {
		_, imgs, err := s.deleteWhere(`pinned = 0 AND id NOT IN (
			SELECT id FROM clips WHERE pinned = 0 ORDER BY last_used_at DESC, id DESC LIMIT ?)`, maxItems)
		if err != nil {
			return nil, err
		}
		images = append(images, imgs...)
	}
	return images, nil
}

// remove deletes clip id and returns its image file name.
func (s *store) remove(id int64) (found bool, image string, err error) {
	n, images, err := s.deleteWhere(`id = ?`, id)
	if len(images) > 0 {
		image = images[0]
	}
	return n > 0, image, err
}

// clearUnpinned deletes every unpinned clip.
func (s *store) clearUnpinned() (int, []string, error) {
	return s.deleteWhere(`pinned = 0`)
}

func (s *store) setPinned(id int64, pinned bool) (bool, error) {
	res, err := s.db.Exec(`UPDATE clips SET pinned = ? WHERE id = ?`, pinned, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

const clipColumns = `id, kind, text, files, image, width, height, size, source_app, created_at, last_used_at, pinned`

// list returns up to limit clips matching query, pinned first, then most
// recently used first.
func (s *store) list(ctx context.Context, query string, limit int) ([]clip, error) {
	q := `SELECT ` + clipColumns + ` FROM clips`
	var args []any
	if cond, condArgs := searchFilter(query); cond != "" {
		q += ` WHERE id IN (SELECT rowid FROM clips_fts WHERE ` + cond + `)`
		args = condArgs
	}
	q += ` ORDER BY pinned DESC, last_used_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clip
	for rows.Next() {
		var c clip
		var files string
		var created, used int64
		if err := rows.Scan(&c.ID, &c.Kind, &c.Text, &files, &c.Image, &c.Width, &c.Height,
			&c.Size, &c.SourceApp, &created, &used, &c.Pinned); err != nil {
			return nil, err
		}
		if files != "" {
			if err := json.Unmarshal([]byte(files), &c.Files); err != nil {
				return nil, fmt.Errorf("clip %d: files: %w", c.ID, err)
			}
		}
		c.CreatedAt, c.LastUsedAt = time.UnixMilli(created), time.UnixMilli(used)
		out = append(out, c)
	}
	return out, rows.Err()
}

// searchFilter turns the search text into a condition on clips_fts. Every
// word must occur as a substring. Words of three or more characters use the
// trigram index through MATCH; shorter ones, which trigrams cannot express,
// use LIKE.
func searchFilter(query string) (string, []any) {
	var phrases, conds []string
	var args []any
	for _, w := range strings.Fields(query) {
		if utf8.RuneCountInString(w) >= 3 {
			phrases = append(phrases, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
			continue
		}
		conds = append(conds, `body LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(w)+"%")
	}
	if len(phrases) > 0 {
		conds = append([]string{`clips_fts MATCH ?`}, conds...)
		args = append([]any{strings.Join(phrases, " AND ")}, args...)
	}
	return strings.Join(conds, " AND "), args
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }
