package dispatch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// IDLen is an id's length: 16 lower-case hex characters from 8 random
// bytes, the shape of a session id. Every row but a person's has one; a
// person is its name.
const IDLen = 16

// newID returns a new opaque id.
func newID() string {
	var b [IDLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("dispatch: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// validID reports whether id is an id's shape (IDLen lower-case hex
// characters): what a query names a label, a milestone or a cursor's row
// by.
func validID(id string) bool {
	if len(id) != IDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// KeyPattern is a project's key: one upper-case letter, then one to five
// upper-case letters or digits (CND). It is chosen at creation and never
// changed.
var KeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,5}$`)

// ValidKey reports whether key is a project's key.
func ValidKey(key string) bool { return KeyPattern.MatchString(key) }

// TicketKey is a ticket's key: its project's key and its number (CND-12),
// unique and never reassigned, kept by a soft delete.
func TicketKey(project string, n int64) string {
	return project + "-" + strconv.FormatInt(n, 10)
}

// ParseKey splits a ticket's key (CND-12) into its project's key and its
// number; ok is false for anything else: a number is digits without a
// leading zero, 1 or more, of at most 18 digits.
func ParseKey(key string) (project string, n int64, ok bool) {
	project, digits, found := strings.Cut(key, "-")
	if !found || !ValidKey(project) || digits == "" || len(digits) > 18 || digits[0] == '0' {
		return "", 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", 0, false
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 1 {
		return "", 0, false
	}
	return project, n, true
}

// errNoProject says the project has no row to allocate from.
var errNoProject = errors.New("dispatch: no such project")

// allocateNumber allocates the next number of the project inside t's
// transaction: next_number read and bumped under the write lock, so
// numbers are allocated in order and never twice, whichever process
// creates. number_from and number_to, reserved for replication, are 0:
// the whole range is this store's (docs/plans/dispatch.md, the allocation
// class of the field rules).
func allocateNumber(t *tx, projectID string) (int64, error) {
	var n int64
	err := t.tx.QueryRowContext(t.ctx, "UPDATE projects SET next_number = next_number + 1 WHERE id = ? RETURNING next_number - 1", projectID).Scan(&n)
	if err != nil {
		if isNoRows(err) {
			return 0, errNoProject
		}
		return 0, err
	}
	return n, nil
}
