// Package conductor holds what the repository's root puts in the binary:
// the third-party notices, served at /third-party-notices.txt.
package conductor

import _ "embed"

// ThirdPartyNotices is THIRD_PARTY_NOTICES (scripts/notices.py writes it):
// the licences of the Go modules, npm packages and fonts the binary and the
// desktop app ship.
//
//go:embed THIRD_PARTY_NOTICES
var ThirdPartyNotices string
