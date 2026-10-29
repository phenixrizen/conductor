package dispatch

import (
	"database/sql"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

// The bounds of the fields this change's rows carry, in characters; the
// row types and bounds of projects, tickets and the rest come with them
// (docs/plans/dispatch.md, PR-2).
const (
	// MaxPersonName bounds a person's name.
	MaxPersonName = 60
)

// checkName checks a name or a title: 1 to max characters of valid UTF-8,
// none of them a control character. The shape wrong is invalid; past the
// length is bound.
func checkName(field, s string, max int) error {
	if s == "" {
		return refuse(RefusalInvalid, "%s is empty", field)
	}
	if !utf8.ValidString(s) {
		return refuse(RefusalInvalid, "%s is not valid UTF-8", field)
	}
	if utf8.RuneCountInString(s) > max {
		return refuse(RefusalBound, "%s is more than %d characters", field, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return refuse(RefusalInvalid, "%s holds a control character", field)
		}
	}
	return nil
}

// stampLayout is a time as the store writes it: RFC 3339 in UTC with
// nine fractional digits always, so that stamps order as text (RFC3339Nano
// drops trailing zeros, and ".5Z" would sort after ".50000001Z").
const stampLayout = "2006-01-02T15:04:05.000000000Z"

// stamp is t as the store writes it; the zero time is "".
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(stampLayout)
}

// unstamp reads a stamp back; "" and anything unreadable are the zero
// time.
func unstamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
