package pty

import (
	"slices"
	"testing"
)

func TestBuildEnv(t *testing.T) {
	parent := []string{
		"PATH=/bin", "HOME=/home/x", "SECRET=1", "LC_ALL=C", "CONDUCTOR_ADMIN_TOKEN=t",
		"LD_PRELOAD=/evil.so", "EXTRA=yes", "TERM=dumb",
	}
	env := BuildEnv(parent, []string{"EXTRA"}, map[string]string{"FOO": "bar", "CONDUCTOR_X": "no", "LD_LIBRARY_PATH": "/x"}, Inject("s1", "http://c/api/sessions/s1/attention", "tok"))
	want := []string{"COLORTERM=truecolor", "CONDUCTOR_NOTIFY_TOKEN=tok", "CONDUCTOR_NOTIFY_URL=http://c/api/sessions/s1/attention", "CONDUCTOR_SESSION_ID=s1", "EXTRA=yes", "FOO=bar", "HOME=/home/x", "LC_ALL=C", "PATH=/bin", "TERM=xterm-256color"}
	if !slices.Equal(env, want) {
		t.Fatalf("got %v\nwant %v", env, want)
	}
}
