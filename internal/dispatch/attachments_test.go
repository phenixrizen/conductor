package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The attachments' files (docs/plans/dispatch.md, PR-7): written and
// hashed before their row, held by a lease from before their
// publication, shared by content, deleted by the sweep alone. The tests
// that pause an upload across a second store's sweep are in sweep_test.go,
// the directory syncs in attachments_unix_test.go.

// sumOf is content's hash, the name of its file.
func sumOf(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// rootOf is the attachments' directory of s.
func rootOf(s *Store) string { return filepath.Join(s.dir, attachmentsDir) }

// contentAt is where s keeps the content with the hash.
func contentAt(s *Store, sum string) string { return contentPath(rootOf(s), sum) }

// exists reports whether something is at path.
func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// tempFiles is the names in the uploads' directory of s.
func tempFiles(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(rootOf(s), uploadsDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// contentFiles is the content files of s, by hash, wherever they are.
func contentFiles(t *testing.T, s *Store) []string {
	t.Helper()
	contents, _, err := listAttachments(rootOf(s))
	if err != nil {
		t.Fatal(err)
	}
	var sums []string
	for _, c := range contents {
		sums = append(sums, c.sum)
	}
	return sums
}

// put uploads content to the ticket with the key as name, text/plain,
// under the op id.
func put(s *Store, id, key, name string, content []byte) (Receipt, Attachment, error) {
	return s.AddAttachment(context.Background(), testOp(id), key, name, "text/plain", bytes.NewReader(content))
}

// mustPut is put, failing the test unless it was done now.
func mustPut(t *testing.T, s *Store, id, key, name string, content []byte) Attachment {
	t.Helper()
	rc, a, err := put(s, id, key, name, content)
	want(t, rc, err, StatusDone, "")
	if rc.Repeat || a.ID == "" || rc.ID != a.ID {
		t.Fatalf("%s: %+v %+v", id, rc, a)
	}
	return a
}

// readContent opens the attachment with the id and reads its content.
func readContent(t *testing.T, s *Store, id string) []byte {
	t.Helper()
	_, f, err := s.OpenAttachment(context.Background(), id)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// attachmentTicket is a store under a fresh directory with a project CND
// and a ticket of it, and the ticket's key.
func attachmentTicket(t *testing.T) (*Store, string) {
	t.Helper()
	s := openStore(t, storeDir(t))
	newProject(t, s, "CND")
	_, key := newTicket(t, s, "CND", "Attached")
	return s, key
}

// An upload writes its file and hashes it before anything names it, takes
// its lease before the file is published under the hash, and publishes it
// before the operation writes its row: at beforePublish the lease names
// the hash and the temp file holds the content, nothing published and no
// row; at beforeCommit the content file holds it, the lease still naming
// it, no temp file and no row; after it, the row, no lease and one add row
// of the ticket's attachments field, its value the attachment whole, and
// one commit's change for the ticket.
func TestAnAttachmentIsHashedBeforeItsRowAndLeasedBeforeItIsPublished(t *testing.T) {
	s, key := attachmentTicket(t)
	content := []byte("the build log\n")
	sum := sumOf(content)
	seen := map[string]bool{}
	s.beforePublish = func() {
		seen["beforePublish"] = true
		if n := count(t, s, "SELECT COUNT(*) FROM uploads WHERE sha256 = ?", sum); n != 1 {
			t.Errorf("before the publication: %d leases of the hash", n)
		}
		temps := tempFiles(t, s)
		if len(temps) != 1 {
			t.Errorf("before the publication: the temp files %v", temps)
			return
		}
		if b, err := os.ReadFile(filepath.Join(rootOf(s), uploadsDir, temps[0])); err != nil || sumOf(b) != sum {
			t.Errorf("the temp file: %q %v", b, err)
		}
		if exists(t, contentAt(s, sum)) || count(t, s, "SELECT COUNT(*) FROM attachments") != 0 {
			t.Error("published or named before its lease's publication")
		}
	}
	s.beforeCommit = func() {
		seen["beforeCommit"] = true
		if n := count(t, s, "SELECT COUNT(*) FROM uploads WHERE sha256 = ?", sum); n != 1 {
			t.Errorf("before the commit: %d leases of the hash", n)
		}
		if b, err := os.ReadFile(contentAt(s, sum)); err != nil || !bytes.Equal(b, content) {
			t.Errorf("before the commit, the content: %q %v", b, err)
		}
		if temps := tempFiles(t, s); len(temps) != 0 || count(t, s, "SELECT COUNT(*) FROM attachments") != 0 {
			t.Errorf("before the commit: the temp files %v, or a row", temps)
		}
	}
	commits := listen(s)
	before := ticketOf(t, s, key).Rev
	a := mustPut(t, s, "a1", key, "build.log", content)
	if !seen["beforePublish"] || !seen["beforeCommit"] {
		t.Fatalf("the seams ran: %v", seen)
	}
	tk := ticketOf(t, s, key)
	if a.TicketID != tk.ID || a.Name != "build.log" || a.Size != int64(len(content)) || a.SHA256 != sum || a.ContentType != "text/plain" || a.CreatedBy != "Tester" ||
		a.CreatedAt.IsZero() || !a.DeletedAt.IsZero() {
		t.Fatalf("the attachment: %+v", a)
	}
	if read, err := s.Attachment(context.Background(), a.ID); err != nil || read != a {
		t.Fatalf("the row: %+v %v, want %+v", read, err, a)
	}
	if tk.Rev != before+1 {
		t.Fatalf("the ticket's rev %d after %d", tk.Rev, before)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM uploads"); n != 0 {
		t.Fatalf("%d leases left", n)
	}
	if !bytes.Equal(readContent(t, s, a.ID), content) {
		t.Fatal("the content did not read back whole")
	}
	rows := changesOf(t, s, "test", "a1")
	if len(rows) != 1 || rows[0].Entity != EntityTicket || rows[0].EntityID != tk.ID || rows[0].Field != "attachments" || rows[0].Op != OpAdd {
		t.Fatalf("the change rows: %+v", rows)
	}
	var value Attachment
	if err := json.Unmarshal([]byte(rows[0].Value), &value); err != nil || value != a {
		t.Fatalf("the add's value %s: %v", rows[0].Value, err)
	}
	if cs := commits.one(t); len(cs) != 1 || cs[0].Entity != EntityTicket || cs[0].ID != tk.ID || cs[0].Ticket != tk.ID || cs[0].Project != "CND" {
		t.Fatalf("the commit's changes: %+v", cs)
	}
	if v, err := s.Ticket(context.Background(), key); err != nil || len(v.Attachments) != 1 || v.Attachments[0] != a {
		t.Fatalf("the ticket's read: %+v %v", v.Attachments, err)
	}
}

// One content uploaded twice is one file, two rows naming it. The same op
// id and content again answers the receipt, writing nothing more and
// leaving no lease; other content under it is op_conflict, its file then
// named by nothing, which the sweep deletes.
func TestASecondUploadOfTheSameContentSharesTheFile(t *testing.T) {
	s, key := attachmentTicket(t)
	_, other := newTicket(t, s, "CND", "Also attached")
	content := []byte("shared")
	first := mustPut(t, s, "a1", key, "one.txt", content)
	second := mustPut(t, s, "a2", other, "two.txt", content)
	if first.SHA256 != second.SHA256 || first.ID == second.ID {
		t.Fatalf("two uploads of one content: %+v %+v", first, second)
	}
	if files := contentFiles(t, s); !slices.Equal(files, []string{first.SHA256}) {
		t.Fatalf("the content files: %v", files)
	}
	rc, a, err := put(s, "a1", key, "one.txt", content)
	want(t, rc, err, StatusDone, "")
	if !rc.Repeat || rc.ID != first.ID || a.ID != "" {
		t.Fatalf("the repeat: %+v %+v", rc, a)
	}
	rc, _, err = put(s, "a1", key, "one.txt", []byte("other content"))
	want(t, rc, err, StatusFailed, RefusalOpConflict)
	if n := count(t, s, "SELECT COUNT(*) FROM attachments"); n != 2 {
		t.Fatalf("%d attachments", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM uploads"); n != 0 {
		t.Fatalf("%d leases left by a repeat and a conflict", n)
	}
	orphan := sumOf([]byte("other content"))
	if !exists(t, contentAt(s, orphan)) {
		t.Fatal("the conflict's content was never published")
	}
	if err := s.sweep(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if files := contentFiles(t, s); !slices.Equal(files, []string{first.SHA256}) {
		t.Fatalf("after the sweep, the content files: %v", files)
	}
}

// A remove tombstones the row, records its remove with the tag of the add
// it removes, and deletes no file; the sweep after it deletes the file no
// live row names and leaves one another ticket's live row shares. A
// removed attachment opens as not found and is removed once.
func TestARemoveDeletesNoFileAndTheSweepCollectsWhatNoRowNames(t *testing.T) {
	ctx := context.Background()
	s, key := attachmentTicket(t)
	_, other := newTicket(t, s, "CND", "Shares one")
	shared, alone := []byte("shared content"), []byte("its own content")
	gone := mustPut(t, s, "a1", key, "shared.txt", shared)
	kept := mustPut(t, s, "a2", other, "shared.txt", shared)
	own := mustPut(t, s, "a3", key, "own.txt", alone)
	commits := listen(s)
	for i, a := range []Attachment{gone, own} {
		rc, err := s.RemoveAttachment(ctx, testOp(opID("r", i)), key, a.ID)
		want(t, rc, err, StatusDone, "")
		if cs := commits.one(t); len(cs) != 1 || cs[0].Entity != EntityTicket || cs[0].Ticket != gone.TicketID {
			t.Fatalf("a remove's changes: %+v", cs)
		}
	}
	rows := changesOf(t, s, "test", "r0")
	if len(rows) != 1 || rows[0].Op != OpRemove || rows[0].Field != "attachments" || rows[0].Prior != EncodeTags([]Tag{{Actor: "test", OpID: "a1", N: 0}}) {
		t.Fatalf("the remove's rows: %+v", rows)
	}
	if a, err := s.Attachment(ctx, gone.ID); err != nil || a.DeletedAt.IsZero() {
		t.Fatalf("the removed row: %+v %v", a, err)
	}
	for _, sum := range []string{gone.SHA256, own.SHA256} {
		if !exists(t, contentAt(s, sum)) {
			t.Fatal("a remove deleted a file")
		}
	}
	if _, _, err := s.OpenAttachment(ctx, own.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed attachment opened: %v", err)
	}
	rc, err := s.RemoveAttachment(ctx, testOp("r9"), key, own.ID)
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.RemoveAttachment(ctx, testOp("r10"), key, kept.ID)
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.RemoveAttachment(ctx, testOp("r11"), key, newID())
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if err := s.sweep(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(t, contentAt(s, own.SHA256)) || !exists(t, contentAt(s, kept.SHA256)) {
		t.Fatal("the sweep deleted the wrong file")
	}
	if !bytes.Equal(readContent(t, s, kept.ID), shared) {
		t.Fatal("the shared content did not read back whole")
	}
	if v, err := s.Ticket(ctx, key); err != nil || len(v.Attachments) != 0 {
		t.Fatalf("the ticket's attachments after the removes: %+v %v", v.Attachments, err)
	}
}

// The operation's rules, each a failed receipt that leaves no lease: a
// name with '/', '\', a control character, "." or ".." invalid, as an
// empty one; a content type past 100 bytes bound; a ticket missing or
// deleted not_found, an archived project archived, a revision behind
// stale_revision; the 51st live attachment bound, while a removed one
// counts for nothing. A refusal's content is named by no row.
func TestAnAttachmentRefusedLeavesNoLease(t *testing.T) {
	ctx := context.Background()
	s, key := attachmentTicket(t)
	refused := func(id, key, name, contentType string, op Op, refusal string) {
		t.Helper()
		op.ID = id
		rc, a, err := s.AddAttachment(ctx, op, key, name, contentType, strings.NewReader("content of "+id))
		want(t, rc, err, StatusFailed, refusal)
		if a.ID != "" {
			t.Fatalf("%s: an attachment returned: %+v", id, a)
		}
		if n := count(t, s, "SELECT COUNT(*) FROM uploads"); n != 0 {
			t.Fatalf("%s: %d leases left", id, n)
		}
	}
	for i, name := range []string{"a/b.txt", `a\b.txt`, "..", ".", "", "bell\a.txt", strings.Repeat("n", MaxAttachmentName+1)} {
		code := RefusalInvalid
		if len(name) > MaxAttachmentName {
			code = RefusalBound
		}
		refused(opID("n", i), key, name, "text/plain", testOp(""), code)
	}
	refused("t1", key, "type.txt", strings.Repeat("t", MaxContentType+1), testOp(""), RefusalBound)
	refused("k1", "CND-99", "missing.txt", "", testOp(""), RefusalNotFound)
	stale := testOp("")
	stale.ExpectedRev = ticketOf(t, s, key).Rev + 1
	refused("v1", key, "stale.txt", "", stale, RefusalStaleRevision)
	_, deleted := newTicket(t, s, "CND", "Deleted")
	rc, err := s.DeleteTicket(ctx, testOp("d1"), deleted)
	want(t, rc, err, StatusDone, "")
	refused("k2", deleted, "deleted.txt", "", testOp(""), RefusalNotFound)
	// 50 live, one of them removed first and a 51st taking its place.
	var removed Attachment
	for i := range MaxAttachments + 1 {
		a := mustPut(t, s, opID("m", i), key, fmt.Sprintf("file%d.txt", i), []byte(fmt.Sprintf("content %d", i)))
		if i == 0 {
			removed = a
			rc, err := s.RemoveAttachment(ctx, testOp("r0"), key, a.ID)
			want(t, rc, err, StatusDone, "")
		}
	}
	refused("m51", key, "one.too.many", "", testOp(""), RefusalBound)
	if n := count(t, s, "SELECT COUNT(*) FROM attachments WHERE deleted_at = ''"); n != MaxAttachments {
		t.Fatalf("%d live attachments", n)
	}
	rc, err = s.ArchiveProject(ctx, testOp("p1"), "CND")
	want(t, rc, err, StatusDone, "")
	refused("p2", key, "archived.txt", "", testOp(""), RefusalArchived)
	// Every refusal's content, and the removed attachment's, is an orphan.
	if err := s.sweep(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if files := contentFiles(t, s); len(files) != MaxAttachments || slices.Contains(files, removed.SHA256) {
		t.Fatalf("%d content files after the sweep", len(files))
	}
}

// Content past MaxAttachmentSize is refused bound, a failed receipt kept,
// with no file left: no temp file, no content file, no lease.
func TestContentPastItsBoundIsRefusedWithNoFileLeft(t *testing.T) {
	s, key := attachmentTicket(t)
	rc, a, err := put(s, "big", key, "big.bin", bytes.Repeat([]byte{'x'}, MaxAttachmentSize+1))
	want(t, rc, err, StatusFailed, RefusalBound)
	if a.ID != "" || len(tempFiles(t, s)) != 0 || len(contentFiles(t, s)) != 0 || count(t, s, "SELECT COUNT(*) FROM uploads") != 0 {
		t.Fatalf("after the refusal: %+v, temp files %v, content files %v", a, tempFiles(t, s), contentFiles(t, s))
	}
	if kept, found, err := s.Receipt(context.Background(), "test", "big"); err != nil || !found || kept.Refusal != RefusalBound {
		t.Fatalf("the refusal's receipt: %+v %v %v", kept, found, err)
	}
	// At its bound, it is taken whole.
	full := bytes.Repeat([]byte{'y'}, MaxAttachmentSize)
	at := mustPut(t, s, "full", key, "full.bin", full)
	if at.Size != MaxAttachmentSize || !bytes.Equal(readContent(t, s, at.ID), full) {
		t.Fatalf("an attachment at its bound: %+v", at)
	}
}

// A store that opened read-only takes no upload: a read_only refusal,
// written nowhere, its content never read and no directory made. An
// operation the store would not look at is an error before the content is
// read.
func TestAReadOnlyStoreTakesNoUpload(t *testing.T) {
	dir := storeDir(t)
	s := openStore(t, dir)
	newProject(t, s, "CND")
	if _, err := s.db.Exec("UPDATE schema_version SET version = ?", Version+1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.RemoveAll(filepath.Join(dir, attachmentsDir)); err != nil {
		t.Fatal(err)
	}
	ro := openStore(t, dir)
	var read bytes.Buffer
	rc, _, err := ro.AddAttachment(context.Background(), testOp("a1"), "CND-1", "x.txt", "", io.TeeReader(strings.NewReader("content"), &read))
	want(t, rc, err, StatusFailed, RefusalReadOnly)
	if read.Len() != 0 || exists(t, filepath.Join(dir, attachmentsDir)) {
		t.Fatalf("a read-only store read %d bytes or made its directory", read.Len())
	}
	w := openStore(t, storeDir(t))
	for _, op := range []Op{{Actor: "test", ID: "bad id"}, {Actor: "test", ID: "a1", By: strings.Repeat("n", MaxByName+1)}} {
		if _, _, err := w.AddAttachment(context.Background(), op, "CND-1", "x.txt", "", io.TeeReader(strings.NewReader("content"), &read)); err == nil || read.Len() != 0 {
			t.Fatalf("%+v: %v, %d bytes read", op, err, read.Len())
		}
	}
}

// A failure of the content's reader is the upload's error: nothing kept,
// no temp file, no lease.
func TestAnUploadWhoseReaderFailsKeepsNothing(t *testing.T) {
	s, key := attachmentTicket(t)
	broken := errors.New("the client went away")
	_, _, err := s.AddAttachment(context.Background(), testOp("a1"), key, "x.txt", "", io.MultiReader(strings.NewReader("half"), iotestErr{broken}))
	if !errors.Is(err, broken) {
		t.Fatalf("the reader's failure: %v", err)
	}
	if _, found, _ := s.Receipt(context.Background(), "test", "a1"); found || len(tempFiles(t, s)) != 0 || count(t, s, "SELECT COUNT(*) FROM uploads") != 0 {
		t.Fatal("a failed read kept something")
	}
}

// iotestErr is a reader that fails with its error.
type iotestErr struct{ err error }

func (r iotestErr) Read([]byte) (int, error) { return 0, r.err }

// OpenAttachment opens a live attachment's content by its hash, of a
// deleted ticket's too; an id unknown is not found; content missing, or
// not of the row's size, is an error.
func TestOpenAttachmentReadsTheContentItsRowNames(t *testing.T) {
	ctx := context.Background()
	s, key := attachmentTicket(t)
	a := mustPut(t, s, "a1", key, "notes.txt", []byte("notes"))
	rc, err := s.DeleteTicket(ctx, testOp("d1"), key)
	want(t, rc, err, StatusDone, "")
	if !bytes.Equal(readContent(t, s, a.ID), []byte("notes")) {
		t.Fatal("a deleted ticket's attachment did not open")
	}
	if _, _, err := s.OpenAttachment(ctx, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
	if err := os.WriteFile(contentAt(s, a.SHA256), []byte("not the notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenAttachment(ctx, a.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("content of another size: %v", err)
	}
	if err := os.Remove(contentAt(s, a.SHA256)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenAttachment(ctx, a.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("content missing: %v", err)
	}
}
