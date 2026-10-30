package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rocksolidlabs/conductor/internal/dispatch"
)

// A ticket's attachments over HTTP (docs/plans/dispatch.md, PR-7): the
// upload, multipart and bounded; the download and its headers; the
// removal; expired through the store's seam; their events.

// uploadBoundary is the boundary of the tests' uploads.
const uploadBoundary = "conductor-upload-boundary"

// uploadOf is an upload's body, multipart/form-data: a preamble of pad
// bytes (lines the reader skips, which count toward the body's bound),
// the fields given in their order, then the file part, named filename, of
// the type given, holding content. It returns the body and its
// Content-Type.
func uploadOf(t *testing.T, pad int, fields [][2]string, filename, contentType string, content []byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	for pad > 0 {
		line := min(pad, 1000)
		b.WriteString(strings.Repeat("p", line-1) + "\n")
		pad -= line
	}
	mw := multipart.NewWriter(&b)
	if err := mw.SetBoundary(uploadBoundary); err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(filename)+`"`)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	w, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), mw.FormDataContentType()
}

// send sends body, of the Content-Type given, with the admin's token, and
// returns the reply's status, its headers, its bytes and the reply
// decoded when it is JSON.
func (e *testEnv) send(t *testing.T, method, path string, body []byte, contentType string) (int, http.Header, []byte, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, e.http.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") && len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("%s %s: the reply is not JSON: %v", method, path, err)
		}
	}
	return resp.StatusCode, resp.Header, data, out
}

// upload sends an upload of content as filename, of the type given, by
// Tester under a new op id, and fails the test unless the reply has the
// status and, for an error, the code; it returns the reply decoded.
func (e *testEnv) upload(t *testing.T, key, filename, contentType string, content []byte, status int, code string) map[string]any {
	t.Helper()
	body, ct := uploadOf(t, 0, [][2]string{{"opId", opID()}, {"byName", "Tester"}}, filename, contentType, content)
	return e.expectUpload(t, key, body, ct, status, code)
}

// expectUpload sends body to the ticket's upload route and fails the test
// unless the reply has the status and, for an error, the code.
func (e *testEnv) expectUpload(t *testing.T, key string, body []byte, contentType string, status int, code string) map[string]any {
	t.Helper()
	got, _, data, out := e.send(t, "POST", "/api/dispatch/tickets/"+key+"/attachments", body, contentType)
	if got != status || (code != "" && errorCode(out) != code) {
		t.Fatalf("upload to %s: %d %.300s, want %d %s", key, got, data, status, code)
	}
	return out
}

// sha is content's hash as the store names it.
func sha(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// The routes: an upload 201 {receipt, attachment} and its repeat 200, the
// ticket's read carrying it; the download byte for byte with its headers
// (nosniff, attachment with the name as filename*, an allowed type as it
// is, an SVG and HTML as application/octet-stream, the length); the
// removal, a tombstone, after which the download is 404; and what the
// store refuses or the route will not read.
func TestDispatchAttachmentRoutes(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	e.newDispatchProject(t, "ATT", nil)
	ticket := e.newDispatchTicket(t, "ATT", "Attached", nil)
	png := []byte("\x89PNG\r\n\x1a\n not really a picture")
	body, ct := uploadOf(t, 0, [][2]string{{"opId", "up1"}, {"byName", "Ann"}}, "shot.png", "image/png", png)
	out := e.expectUpload(t, "ATT-1", body, ct, http.StatusCreated, "")
	rc, a := receiptOf(t, out), entityOf(t, out, "attachment")
	if rc["status"] != "done" || rc["intent"] != "attachment.add" || rc["id"] != a["id"] || a["ticketId"] != ticket["id"] || a["name"] != "shot.png" ||
		a["size"] != float64(len(png)) || a["sha256"] != sha(png) || a["contentType"] != "image/png" || a["createdBy"] != "Ann" {
		t.Fatalf("the upload: %v", out)
	}
	again := e.expectUpload(t, "ATT-1", body, ct, http.StatusOK, "")
	if receiptOf(t, again)["repeat"] != true || entityOf(t, again, "attachment")["id"] != a["id"] {
		t.Fatalf("the repeat: %v", again)
	}
	tk := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/ATT-1", nil, http.StatusOK, ""), "ticket")
	if list := tk["attachments"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != a["id"] {
		t.Fatalf("the ticket's attachments: %v", tk["attachments"])
	}
	download := func(id string, want []byte, contentType, disposition string) {
		t.Helper()
		status, h, data, _ := e.send(t, "GET", "/api/dispatch/attachments/"+id, nil, "")
		if status != http.StatusOK || !bytes.Equal(data, want) {
			t.Fatalf("the download of %s: %d %q", id, status, data)
		}
		if h.Get("Content-Type") != contentType || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Disposition") != disposition ||
			h.Get("Content-Length") != strconv.Itoa(len(want)) {
			t.Fatalf("the download's headers: %v", h)
		}
	}
	download(a["id"].(string), png, "image/png", "attachment; filename*=UTF-8''shot.png")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	logo := entityOf(t, e.upload(t, "ATT-1", "logo.svg", "image/svg+xml", svg, http.StatusCreated, ""), "attachment")
	download(logo["id"].(string), svg, "application/octet-stream", "attachment; filename*=UTF-8''logo.svg")
	page := []byte("<script>alert(1)</script>")
	named := entityOf(t, e.upload(t, "ATT-1", `résumé "v2"; 100%.html`, "text/html", page, http.StatusCreated, ""), "attachment")
	download(named["id"].(string), page, "application/octet-stream", "attachment; filename*=UTF-8''r%C3%A9sum%C3%A9%20%22v2%22%3B%20100%25.html")

	removed := e.expect(t, "POST", "/api/dispatch/tickets/ATT-1/attachments/"+a["id"].(string)+"/delete", map[string]any{"opId": opID()}, http.StatusOK, "")
	if gone := entityOf(t, removed, "attachment"); gone["id"] != a["id"] || gone["deletedAt"] == nil || receiptOf(t, removed)["intent"] != "attachment.remove" {
		t.Fatalf("the removal: %v", removed)
	}
	e.expect(t, "GET", "/api/dispatch/attachments/"+a["id"].(string), nil, http.StatusNotFound, "not_found")
	e.expect(t, "POST", "/api/dispatch/tickets/ATT-1/attachments/"+a["id"].(string)+"/delete", map[string]any{"opId": opID()}, http.StatusNotFound, "not_found")
	e.expect(t, "GET", "/api/dispatch/attachments/0123456789abcdef", nil, http.StatusNotFound, "not_found")
	e.expect(t, "GET", "/api/dispatch/attachments/nope", nil, http.StatusBadRequest, "invalid_request")

	// The store's refusals, each with its receipt.
	missing := e.upload(t, "ATT-9", "x.txt", "text/plain", []byte("x"), http.StatusNotFound, "not_found")
	if receiptOf(t, missing)["refusal"] != "not_found" {
		t.Fatalf("a missing ticket: %v", missing)
	}
	for _, name := range []string{"a/b.txt", "..", ""} {
		e.upload(t, "ATT-1", name, "text/plain", []byte(name), http.StatusConflict, "invalid")
	}
	e.upload(t, "ATT-1", "x.txt", strings.Repeat("t", dispatch.MaxContentType+1), []byte("x"), http.StatusConflict, "bound")

	// What the route will not read: 400, nothing kept.
	file := func(fields ...[2]string) ([]byte, string) {
		return uploadOf(t, 0, fields, "x.txt", "text/plain", []byte("x"))
	}
	refused := func(what string, body []byte, ct, words string) {
		t.Helper()
		out := e.expectUpload(t, "ATT-1", body, ct, http.StatusBadRequest, "invalid_request")
		if msg := out["error"].(map[string]any)["message"].(string); !strings.Contains(msg, words) {
			t.Fatalf("%s: %q, want %q", what, msg, words)
		}
	}
	b, _ := file([2]string{"opId", "j1"})
	refused("a JSON content type", b, "application/json", "multipart/form-data")
	b, ct = file([2]string{"opId", "x y"})
	refused("a bad op id", b, ct, "opId")
	b, ct = file()
	refused("no op id", b, ct, "opId")
	b, ct = file([2]string{"opId", "f1"}, [2]string{"expectedRev", "x"})
	refused("a revision that is none", b, ct, "expectedRev")
	b, ct = file([2]string{"opId", "f2"}, [2]string{"title", "x"})
	refused("an unknown field", b, ct, `"title"`)
	b, ct = file([2]string{"opId", "f3"}, [2]string{"opId", "f4"})
	refused("a field twice", b, ct, "twice")
	b, ct = file([2]string{"opId", "f5"}, [2]string{"byName", strings.Repeat("n", dispatch.MaxByName+1)})
	refused("a display name too long", b, ct, strings.TrimPrefix(dispatch.ErrBadBy.Error(), "dispatch: "))
	b, ct = uploadOf(t, 0, [][2]string{{"opId", "f6"}}, "x.txt", "text/plain", []byte("x"))
	b = bytes.Replace(b, []byte("--"+uploadBoundary+"--"), []byte("--"+uploadBoundary+"\r\nContent-Disposition: form-data; name=\"byName\"\r\n\r\nlate\r\n--"+uploadBoundary+"--"), 1)
	refused("a part after the file", b, ct, "last part")
	var none bytes.Buffer
	mw := multipart.NewWriter(&none)
	if err := mw.WriteField("opId", "f7"); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	refused("no file part", none.Bytes(), mw.FormDataContentType(), "no part named file")
	e.expectUpload(t, "nokey", body, ct, http.StatusBadRequest, "invalid_request")
	for _, id := range []string{"j1", "f1", "f2", "f3", "f5", "f6", "f7"} {
		e.expect(t, "GET", "/api/dispatch/ops/"+id, nil, http.StatusNotFound, "not_found")
	}
	if list := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/ATT-1", nil, http.StatusOK, ""), "ticket")["attachments"].([]any); len(list) != 2 {
		t.Fatalf("%d attachments after the refusals, want the SVG and the page", len(list))
	}
}

// An upload is bounded by its file at the store's bound and the envelope:
// a file of exactly 16 MiB in a body of exactly maxAttachmentBody is
// taken and downloaded whole, a body one byte longer is 413 bound and
// keeps nothing, and a file one byte past 16 MiB, its body within the
// bound, is the store's 409 bound, its receipt kept, no file left. A body
// past its bound by bytes the store would not read is 413 all the same,
// keeping no receipt and no attachment: a file of 17 MiB, whose rest the
// store never reads, and a small file followed past the bound by bytes
// after its body's close.
func TestDispatchAttachmentUploadIsBounded(t *testing.T) {
	t.Parallel()
	// The bound itself, as the plan sets it: the walks below hold the
	// route to whatever the constants say.
	if maxAttachmentEnvelope != 64<<10 || maxAttachmentBody != 16<<20+64<<10 {
		t.Fatalf("an upload's bound is %d bytes, its envelope %d", maxAttachmentBody, maxAttachmentEnvelope)
	}
	e := dispatchEnv(t)
	e.newDispatchProject(t, "BIG", nil)
	e.newDispatchTicket(t, "BIG", "Large files", nil)
	content := bytes.Repeat([]byte("0123456789abcdef"), dispatch.MaxAttachmentSize/16)
	at := func(id string, extra int) ([]byte, string) {
		fields := [][2]string{{"opId", id}, {"byName", "Tester"}}
		body, _ := uploadOf(t, 0, fields, "full.bin", "application/octet-stream", content)
		body, ct := uploadOf(t, maxAttachmentBody-len(body)+extra, fields, "full.bin", "application/octet-stream", content)
		if len(body) != maxAttachmentBody+extra {
			t.Fatalf("a body of %d bytes", len(body))
		}
		return body, ct
	}
	body, ct := at("past", 1)
	e.expectUpload(t, "BIG-1", body, ct, http.StatusRequestEntityTooLarge, "bound")
	e.expect(t, "GET", "/api/dispatch/ops/past", nil, http.StatusNotFound, "not_found")
	// Past its bound whatever else is wrong with it: an op id the route
	// refuses before the file is read is 413 all the same, the rest of
	// the body read first.
	bad := bytes.Replace(body, []byte("past"), []byte("p st"), 1)
	e.expectUpload(t, "BIG-1", bad, ct, http.StatusRequestEntityTooLarge, "bound")
	body, ct = at("full", 0)
	a := entityOf(t, e.expectUpload(t, "BIG-1", body, ct, http.StatusCreated, ""), "attachment")
	if a["size"] != float64(dispatch.MaxAttachmentSize) || a["sha256"] != sha(content) {
		t.Fatalf("the attachment at its bound: %v", a)
	}
	status, _, data, _ := e.send(t, "GET", "/api/dispatch/attachments/"+a["id"].(string), nil, "")
	if status != http.StatusOK || !bytes.Equal(data, content) {
		t.Fatalf("the download at its bound: %d, %d bytes", status, len(data))
	}
	over := append(append([]byte(nil), content...), '!')
	body, ct = uploadOf(t, 0, [][2]string{{"opId", "over"}}, "over.bin", "", over)
	if len(body) > maxAttachmentBody {
		t.Fatalf("the body of a file one byte past its bound is %d bytes", len(body))
	}
	out := e.expectUpload(t, "BIG-1", body, ct, http.StatusConflict, "bound")
	if receiptOf(t, out)["refusal"] != "bound" {
		t.Fatalf("a file past its bound: %v", out)
	}
	huge := bytes.Repeat([]byte{'h'}, 17<<20)
	body, ct = uploadOf(t, 0, [][2]string{{"opId", "huge"}}, "huge.bin", "", huge)
	e.expectUpload(t, "BIG-1", body, ct, http.StatusRequestEntityTooLarge, "bound")
	small := []byte("small")
	body, ct = uploadOf(t, 0, [][2]string{{"opId", "tail"}}, "small.txt", "text/plain", small)
	body = append(body, bytes.Repeat([]byte{'e'}, maxAttachmentBody-len(body)+1)...)
	e.expectUpload(t, "BIG-1", body, ct, http.StatusRequestEntityTooLarge, "bound")
	for _, id := range []string{"huge", "tail"} {
		e.expect(t, "GET", "/api/dispatch/ops/"+id, nil, http.StatusNotFound, "not_found")
	}
	if list := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/BIG-1", nil, http.StatusOK, ""), "ticket")["attachments"].([]any); len(list) != 1 {
		t.Fatalf("%d attachments, want the one at its bound alone", len(list))
	}
	for _, content := range [][]byte{huge, small} {
		if _, err := os.Stat(filepath.Join(e.srv.dispatch.Dir(), "attachments", sha(content)[:2], sha(content))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a body past its bound left its file: %v", err)
		}
	}
	tmp := filepath.Join(e.srv.dispatch.Dir(), "attachments", "tmp")
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("the uploads' directory: %v %v", entries, err)
	}
}

// An upload whose lease is gone when its row would be written, here
// dropped through the store's seam as a second server's sweep would drop
// it, is 409 expired with its failed receipt, which the receipts route
// reads back, and writes no attachment; the same file sent again under a
// new op id is taken.
func TestDispatchAttachmentExpiredThroughTheSeam(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	e.newDispatchProject(t, "EXP", nil)
	e.newDispatchTicket(t, "EXP", "Suspended", nil)
	raw := openRaw(t, filepath.Join(e.srv.dispatch.Dir(), dispatch.File))
	e.srv.dispatch.SetBeforeCommitForTest(func() {
		if _, err := raw.Exec("DELETE FROM uploads"); err != nil {
			t.Error(err)
		}
	})
	body, ct := uploadOf(t, 0, [][2]string{{"opId", "late"}}, "late.txt", "text/plain", []byte("too late"))
	out := e.expectUpload(t, "EXP-1", body, ct, http.StatusConflict, "expired")
	if rc := receiptOf(t, out); rc["status"] != "failed" || rc["refusal"] != "expired" {
		t.Fatalf("the refusal: %v", out)
	}
	if rc := receiptOf(t, e.expect(t, "GET", "/api/dispatch/ops/late", nil, http.StatusOK, "")); rc["refusal"] != "expired" {
		t.Fatalf("the receipt read back: %v", rc)
	}
	if list := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/EXP-1", nil, http.StatusOK, ""), "ticket")["attachments"].([]any); len(list) != 0 {
		t.Fatalf("an expired upload attached %v", list)
	}
	e.srv.dispatch.SetBeforeCommitForTest(nil)
	e.upload(t, "EXP-1", "late.txt", "text/plain", []byte("too late"), http.StatusCreated, "")
}

// An attachment's upload and removal reach the pages as one entry of a
// client's queue holding the event of its ticket, through the path every
// Dispatch commit takes; a refused upload sends nothing.
func TestDispatchAttachmentEventsNameTheirTicket(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	queued := e.srv.events.subscribe()
	defer e.srv.events.unsubscribe(queued)
	e.newDispatchProject(t, "EVA", nil)
	tk := e.newDispatchTicket(t, "EVA", "Evented", nil)
	for len(queued) > 0 {
		<-queued
	}
	want := dispatchEvent{Entity: "ticket", ID: tk["id"].(string), Ticket: tk["id"].(string), Project: "EVA"}
	commit := func(what string) {
		t.Helper()
		if len(queued) != 1 {
			t.Fatalf("%s: %d entries queued", what, len(queued))
		}
		data := dispatchData(t, <-queued)
		var got dispatchEvent
		if len(data) != 1 || json.Unmarshal([]byte(data[0]), &got) != nil {
			t.Fatalf("%s: %v", what, data)
		}
		got.Seq = 0
		if got != want {
			t.Fatalf("%s: %+v, want %+v", what, got, want)
		}
	}
	a := entityOf(t, e.upload(t, "EVA-1", "x.txt", "text/plain", []byte("x"), http.StatusCreated, ""), "attachment")
	commit("an upload")
	e.expect(t, "POST", "/api/dispatch/tickets/EVA-1/attachments/"+a["id"].(string)+"/delete", map[string]any{"opId": opID()}, http.StatusOK, "")
	commit("a removal")
	e.upload(t, "EVA-1", "a/b", "text/plain", []byte("x"), http.StatusConflict, "invalid")
	if len(queued) != 0 {
		t.Fatalf("a refusal queued %d entries", len(queued))
	}
}
