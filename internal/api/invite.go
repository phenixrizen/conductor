package api

import (
	"net/url"
	"strings"
)

// inviteFor is the share link at base and token as an invite the desktop
// app opens itself, conductor://<host>/join/<token>: the app renders the
// join page from its own bundle and signals to the server named. https is
// implied; a plain http base, which the app accepts for loopback alone (a
// development server), carries ?http=1.
func inviteFor(base, token string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	invite := "conductor://" + u.Host + strings.TrimRight(u.Path, "/") + "/join/" + url.PathEscape(token)
	if u.Scheme == "http" {
		invite += "?http=1"
	}
	return invite
}
