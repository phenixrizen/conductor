package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The sweep is the one thing that deletes an attachment's file
// (docs/plans/dispatch.md, "Attachments"). A file is an orphan when
// neither a live attachment row nor an upload's lease names its hash: a
// crash between an upload's publication and its commit (its lease then
// expires), a publication whose sync failed, or a removed attachment's
// content. It runs as two steps, each a write transaction of its own.
// expireLeases drops the leases older than sweepGrace, a crashed upload's,
// and commits before any file goes, so a dropped lease stays dropped
// however the collection ends: a deletion the database undoes cannot
// bring a file back, and an upload paused across the sweep that found
// its lease restored by a rollback would commit a row naming content that
// is not there. collectOrphans then lists the directory and, in a second
// transaction it holds while it unlinks, deletes every listed content
// file that no live row and no lease names, and every upload's temp file
// older than the grace. The write lock orders it against an upload in this
// server or in another on the same directory: an upload publishes its file
// only after its lease is committed, and no commit falls inside the
// collection's transaction, so a collection that began before the lease
// finishes its deletes before the file is published (a reused hash it
// deleted is published afresh), and one that begins after reads the lease
// and leaves the file, whatever its age. An upload whose lease the sweep
// dropped before its row was written is refused expired, never trusted.

// shardPattern is the name of a shard of the attachments' directory: the
// first two characters of its contents' hashes.
var shardPattern = regexp.MustCompile(`^[0-9a-f]{2}$`)

// sweep expires the leases older than sweepGrace, then collects the
// files nothing names (collectOrphans), now being the clock both read.
// Open runs it before any caller can upload, and a test calls it, or
// either step alone. A lease the expiry failed to drop is still a lease,
// its file left too, so the collection runs whatever the expiry did. It
// returns the first error either step met: a file it could not delete is
// left for the next sweep, the sweep going on with the rest.
func (s *Store) sweep(ctx context.Context, now time.Time) error {
	err := s.expireLeases(ctx, now)
	if cerr := s.collectOrphans(ctx, now); err == nil {
		err = cerr
	}
	return err
}

// expireLeases drops the uploads' leases older than sweepGrace, a crashed
// upload's, in one write transaction (BEGIN IMMEDIATE) that it commits,
// on disk (synchronous FULL), before it returns: an upload that resumes
// after it finds no lease and is refused expired.
func (s *Store) expireLeases(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	grace := s.sweepGrace
	s.mu.Unlock()
	if err := s.exec(ctx, "DELETE FROM uploads WHERE created_at <= ?", stamp(now.Add(-grace))); err != nil {
		return fmt.Errorf("dispatch: expiring the uploads' leases: %w", err)
	}
	return nil
}

// collectOrphans lists the attachments' directory, then, in a write
// transaction (BEGIN IMMEDIATE) it holds while it works and writes nothing
// in, deletes every listed content file whose hash neither a live
// attachment row nor a lease names, and every upload's temp file at least
// sweepGrace old by now, each through unlink. A file it cannot delete is
// left for the next sweep, and the first such error is returned at the
// end; the database failing stops it.
func (s *Store) collectOrphans(ctx context.Context, now time.Time) error {
	contents, temps, err := listAttachments(filepath.Join(s.dir, attachmentsDir))
	if err != nil {
		return fmt.Errorf("dispatch: listing the attachments: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return ErrClosed
	case s.readOnly || s.db == nil:
		return fmt.Errorf("%w: %s", ErrReadOnly, s.why)
	}
	dbtx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return cancelledAs(ctx, err)
	}
	// It writes nothing: its end undoes nothing, and nothing it deleted was
	// named while it held the lock.
	defer dbtx.Rollback()
	var first error
	unlink := func(path string) {
		if err := s.unlink(path); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = fmt.Errorf("dispatch: sweeping %s: %w", path, err)
		}
	}
	for _, c := range contents {
		var named bool
		if err := dbtx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM attachments WHERE sha256 = ? AND deleted_at = '')
			OR EXISTS (SELECT 1 FROM uploads WHERE sha256 = ?)`, c.sum, c.sum).Scan(&named); err != nil {
			return cancelledAs(ctx, err)
		}
		if !named {
			unlink(c.path)
		}
	}
	cutoff := now.Add(-s.sweepGrace)
	for _, u := range temps {
		if !u.made.After(cutoff) {
			unlink(u.path)
		}
	}
	return first
}

// listed is a file the sweep may delete: a content file with its hash, or
// an upload's temp file with when it was made.
type listed struct {
	path, sum string
	made      time.Time
}

// listAttachments lists the attachments' directory at root: the content
// files of its shards, each a regular file named by a hash beginning with
// its shard's name, and the temp files of its uploads' directory, each a
// regular file named as receive names one. Anything else is no file of
// the store's, and the sweep leaves it. A directory that does not exist
// holds nothing.
func listAttachments(root string) (contents, temps []listed, err error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || (name != uploadsDir && !shardPattern.MatchString(name)) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			path := filepath.Join(root, name, f.Name())
			switch {
			case !f.Type().IsRegular():
			case name == uploadsDir:
				if made, ok := uploadMade(f.Name()); ok {
					temps = append(temps, listed{path: path, made: made})
				}
			case hashPattern.MatchString(f.Name()) && f.Name()[:2] == name:
				contents = append(contents, listed{path: path, sum: f.Name()})
			}
		}
	}
	return contents, temps, nil
}

// openAttachments is what Open does for the attachments of a writable
// store before any caller can upload: their directories made, the first's
// name made durable (attachmentDirs), and the sweep. It returns the first
// error, the sweep running whatever the directories did.
func (s *Store) openAttachments(ctx context.Context, now time.Time) error {
	_, err := s.attachmentDirs(s.syncDir, true)
	if serr := s.sweep(ctx, now); err == nil {
		err = serr
	}
	return err
}
