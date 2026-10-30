package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Archive never reads or writes the delivery dedup log.
type Archive struct {
	db          *sql.DB
	mu          sync.RWMutex
	lastAttempt string
	lastFailure string
}

type Notice struct {
	ID           int64  `json:"id"`
	SourceID     string `json:"source_id"`
	IdentityKind string `json:"identity_kind"`
	Revision     int    `json:"revision"`
	GUID         string `json:"guid"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	RawXML       string `json:"raw_item_xml"`
	Author       string `json:"author"`
	SourceURL    string `json:"source_url"`
	Published    string `json:"published_raw"`
	Observed     string `json:"first_observed_at"`
	LastObserved string `json:"last_observed_at"`
	Hash         string `json:"content_sha256"`
	FeedURL      string `json:"feed_url"`
}

func openArchive(path string) (*Archive, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("archive path must be absolute")
	}
	// The containing directory must be dedicated to the archive.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("archive directory must be private (0700)")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	a := &Archive{db: db}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version > 1 {
		db.Close()
		return nil, errors.New("unsupported archive schema")
	}
	_, err = db.Exec(`PRAGMA busy_timeout=3000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS notices (
 id INTEGER PRIMARY KEY, source_id TEXT NOT NULL, identity_kind TEXT NOT NULL,
 revision INTEGER NOT NULL, guid TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
 raw_xml TEXT NOT NULL, author TEXT NOT NULL, source_url TEXT NOT NULL, published TEXT NOT NULL,
 observed TEXT NOT NULL, last_observed TEXT NOT NULL, hash TEXT NOT NULL, feed_url TEXT NOT NULL,
 UNIQUE(source_id, revision));
 CREATE INDEX IF NOT EXISTS notices_source ON notices(source_id, revision DESC);
 PRAGMA user_version=1;`)
	if err == nil {
		_, err = db.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES('activated_at',?)`, stamp(time.Now()))
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return a, nil
}
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func digest(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func identity(item Item) (string, string) {
	if item.GUID != "" {
		return digest(defaultFeedURL + "\x00guid\x00" + item.GUID), "guid"
	}
	if item.Link != "" {
		return digest(defaultFeedURL + "\x00link\x00" + item.Link), "link"
	}
	// Without a source identifier, publication+author+title is a best-effort key.
	return digest(defaultFeedURL + "\x00fallback\x00" + item.PubDate + "\x00" + item.Author + "\x00" + item.Title), "publication-author-title"
}
func (a *Archive) failure(code string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastAttempt = stamp(time.Now())
	a.lastFailure = code
}
func (a *Archive) ingest(ctx context.Context, items []Item, now time.Time) error {
	a.failure("archive_write_failed")
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		if !strings.Contains(item.Author, "[English]") {
			continue
		}
		id, kind := identity(item)
		content, _ := json.Marshal(item)
		hash := digest(string(content))
		var prev string
		var rev int
		err = tx.QueryRowContext(ctx, `SELECT hash,revision FROM notices WHERE source_id=? ORDER BY revision DESC LIMIT 1`, id).Scan(&prev, &rev)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if prev == hash {
			_, err = tx.ExecContext(ctx, `UPDATE notices SET last_observed=? WHERE source_id=? AND revision=?`, stamp(now), id, rev)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO notices(source_id,identity_kind,revision,guid,title,body,raw_xml,author,source_url,published,observed,last_observed,hash,feed_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, kind, rev+1, item.GUID, item.Title, item.Description, item.RawXML, item.Author, item.Link, item.PubDate, stamp(now), stamp(now), hash, defaultFeedURL)
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('last_successful_poll',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stamp(now))
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.failure("")
	return nil
}

const noticeColumns = `id,source_id,identity_kind,revision,guid,title,body,raw_xml,author,source_url,published,observed,last_observed,hash,feed_url`

func scanNotice(row interface{ Scan(...any) error }) (Notice, error) {
	var n Notice
	err := row.Scan(&n.ID, &n.SourceID, &n.IdentityKind, &n.Revision, &n.GUID, &n.Title, &n.Body, &n.RawXML, &n.Author, &n.SourceURL, &n.Published, &n.Observed, &n.LastObserved, &n.Hash, &n.FeedURL)
	return n, err
}
func (a *Archive) search(ctx context.Context, q string, after int64, limit int) ([]Notice, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT `+noticeColumns+` FROM notices WHERE id>? AND (?='' OR instr(lower(title || char(10) || body),lower(?))>0) ORDER BY id LIMIT ?`, after, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Notice{}
	for rows.Next() {
		n, err := scanNotice(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
func (a *Archive) status(ctx context.Context) (map[string]any, error) {
	var activated, last string
	var count int64
	if err := a.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='activated_at'`).Scan(&activated); err != nil {
		return nil, err
	}
	err := a.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='last_successful_poll'`).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err = a.db.QueryRowContext(ctx, `SELECT count(*) FROM notices`).Scan(&count); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return map[string]any{"activated_at": activated, "last_successful_poll": last, "revision_count": count, "last_attempt": a.lastAttempt, "last_failure": a.lastFailure, "feed_url": defaultFeedURL, "coverage": "English items observed in periodic RSS snapshots since activation; no backfill or guarantee of complete event coverage", "retention": "indefinite; no automatic pruning"}, nil
}
func archiveError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "archive write failed; notification delivery continues")
	}
}

// List responses are compact projections; detail returns the full original fields.
type NoticeSummary struct {
	ID        int64  `json:"id"`
	SourceID  string `json:"source_id"`
	Revision  int    `json:"revision"`
	Title     string `json:"title_excerpt"`
	Excerpt   string `json:"body_excerpt"`
	Published string `json:"published_excerpt"`
	Observed  string `json:"first_observed_at"`
	Hash      string `json:"content_sha256"`
}

func (a *Archive) searchSummaries(ctx context.Context, q string, after int64, limit int) ([]NoticeSummary, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id,source_id,revision,substr(title,1,512),substr(body,1,2000),substr(published,1,256),observed,hash FROM notices WHERE id>? AND (?='' OR instr(lower(title || char(10) || body),lower(?))>0) ORDER BY id LIMIT ?`, after, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NoticeSummary{}
	for rows.Next() {
		var n NoticeSummary
		if err = rows.Scan(&n.ID, &n.SourceID, &n.Revision, &n.Title, &n.Excerpt, &n.Published, &n.Observed, &n.Hash); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
