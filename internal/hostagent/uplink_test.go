package hostagent

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// A session that already runs (a server's local session) is published to a
// rendezvous through the host protocol: listed there as hosted, typed into
// through its relay, its attention and activity forwarded, and gone from
// there when the publication stops.
func TestPublishServesALocalSessionOverTheRelay(t *testing.T) {
	srv, hs := startServer(t)
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	var pub *Published
	info := session.Info{ID: "local-1", Name: "published cat", Kind: session.KindServer, AgentID: "cat", Command: []string{"/bin/cat"}, Cwd: t.TempDir(), Status: session.StatusRunning, Cols: 80, Rows: 24, CreatedAt: time.Now().UTC()}
	local := session.NewLocal(info, proc, session.Options{
		Transport: proto.TransportWS,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnChange: func(i session.Info) {
			if pub != nil {
				pub.OnChange(i)
			}
		},
		OnActivity: func(id string, e session.ActivityEntry, st session.AttentionState) {
			if pub != nil {
				pub.OnActivity(id, e, st)
			}
		},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = local.Stop(ctx)
	})
	u := &Uplink{ServerURL: hs.URL, Token: hostToken, HostName: "office", RelayOnly: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pub, err = u.Publish(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if pub.SessionID == "" || pub.SessionID == "local-1" || pub.ShareBaseURL == "" {
		t.Fatalf("published %+v", pub)
	}
	d, ok := srv.Registry().Get(pub.SessionID)
	if !ok || d.Info().Kind != session.KindHosted || d.Info().Name != "published cat" || d.Info().HostName != "office" || d.Info().AgentID != "cat" {
		t.Fatalf("not listed at the rendezvous: %v %+v", ok, d)
	}

	v := dialViewer(t, hs.URL, pub.SessionID, adminToken)
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	v.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	v.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 90, Rows: 25}))
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	v.expectJSON(proto.TypeControl, proto.CtlReady)
	v.send(proto.Encode(proto.TypeInput, []byte("through the rendezvous\n")))
	v.expectOutput("through the rendezvous")

	// Attention set on the local session reaches the rendezvous.
	local.SetAttention(session.AttentionNeedsInput, "pick one", session.SourceAPI)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && d.Info().Attention.Message != "pick one" {
		time.Sleep(20 * time.Millisecond)
	}
	if d.Info().Attention.Message != "pick one" {
		t.Fatalf("attention at the rendezvous %+v", d.Info().Attention)
	}

	// Stopping the publication takes the session off the rendezvous while
	// the local session goes on.
	pub.Stop()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if i := d.Info(); i.Status.Ended() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if i := d.Info(); !i.Status.Ended() {
		t.Fatalf("rendezvous still shows %+v", i.Status)
	}
	if local.Info().Status != session.StatusRunning {
		t.Fatalf("the local session was stopped by the publication's end: %s", local.Info().Status)
	}
}
