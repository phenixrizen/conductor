package signal

import (
	"errors"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

func register(t *testing.T, hub *Hub) (*HostedSession, *HostConn) {
	t.Helper()
	conn := NewHostConn()
	hs, resumed, err := hub.Register(proto.Register{T: proto.HostRegister, Proto: 1,
		Host: proto.HostInfo{Name: "laptop"}, Session: proto.HostSession{Command: []string{"bash"}, Cols: 80, Rows: 24}}, conn)
	if err != nil || resumed {
		t.Fatalf("register: %v %v", err, resumed)
	}
	return hs, conn
}

func drainText(t *testing.T, conn *HostConn, want string) {
	t.Helper()
	select {
	case o := <-conn.Send:
		if o.Text == nil {
			t.Fatalf("expected text %s, got binary", want)
		}
		if tt, _ := proto.ParseHeader(o.Text); tt != want {
			t.Fatalf("expected %s, got %s", want, tt)
		}
	case <-time.After(time.Second):
		t.Fatalf("no %s message", want)
	}
}

func TestRegisterResumeAndRelayRules(t *testing.T) {
	reg := session.NewRegistry(4)
	hub := NewHub(reg, nil)
	hs, conn := register(t, hub)
	if hs.Info().Name != "bash @ laptop" || hs.Info().Kind != session.KindHosted {
		t.Fatalf("info %+v", hs.Info())
	}
	if _, _, err := hub.Register(proto.Register{Proto: 1}, NewHostConn()); !errors.Is(err, ErrBadRegister) {
		t.Fatalf("bad register: %v", err)
	}

	v, err := hs.AddViewer("0123456789abcdef", session.RoleView, "l1")
	if err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostViewerJoin)
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeInput, Payload: []byte("x")}); err == nil {
		t.Fatal("relay before relay mode must fail")
	}
	if err := hs.StartRelay(v); err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostRelayStart)
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeInput, Payload: []byte("x")}); !errors.Is(err, session.ErrReadOnly) {
		t.Fatalf("view input: %v", err)
	}
	resize := proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: 1, Rows: 1})
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeControl, Payload: resize[1:]}); !errors.Is(err, session.ErrReadOnly) {
		t.Fatalf("view resize: %v", err)
	}
	ping := proto.MustControl(proto.Ping{T: proto.CtlPing})
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeControl, Payload: ping[1:]}); err != nil {
		t.Fatalf("view ping: %v", err)
	}
	select {
	case o := <-conn.Send:
		if o.Binary == nil {
			t.Fatal("expected relay envelope")
		}
		id, inner, err := proto.DecodeRelay(o.Binary[1:])
		if err != nil || id != v.ID || inner.Type != proto.TypeControl {
			t.Fatalf("envelope %s %+v %v", id, inner, err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay envelope not queued")
	}
	hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: []byte("o")})
	select {
	case f := <-v.Out:
		if f[0] != proto.TypeOutput {
			t.Fatalf("viewer frame %v", f)
		}
	default:
		t.Fatal("viewer did not receive relayed frame")
	}
	hs.DisconnectLink("l1")
	if !errors.Is(v.Reason(), session.ErrRevoked) {
		t.Fatalf("revoke reason %v", v.Reason())
	}

	// Host drop closes viewers and marks the session; resume with the secret works once.
	v2, _ := hs.AddViewer("fedcba9876543210", session.RoleControl, "")
	hs.HostDisconnected(conn)
	if !errors.Is(v2.Reason(), ErrHostGone) || hs.Info().Status != session.StatusHostDisconnected {
		t.Fatalf("after disconnect: %v %s", v2.Reason(), hs.Info().Status)
	}
	if _, _, err := hub.Register(proto.Register{Proto: 1, Session: proto.HostSession{Command: []string{"bash"}},
		Resume: &proto.HostResume{SessionID: hs.Info().ID, Secret: "wrong"}}, NewHostConn()); !errors.Is(err, ErrBadResume) {
		t.Fatalf("wrong secret: %v", err)
	}
	conn2 := NewHostConn()
	hs2, resumed, err := hub.Register(proto.Register{Proto: 1, Session: proto.HostSession{Command: []string{"bash"}, Cols: 10, Rows: 10},
		Resume: &proto.HostResume{SessionID: hs.Info().ID, Secret: hs.Secret()}}, conn2)
	if err != nil || !resumed || hs2 != hs || hs.Info().Status != session.StatusRunning || hs.Info().Cols != 10 {
		t.Fatalf("resume: %v %v %+v", err, resumed, hs.Info())
	}
	if err := hs.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	drainText(t, conn2, proto.HostStop)

	// Expiry removes sessions whose host stayed away past the grace period.
	hs.HostDisconnected(conn2)
	hub.Expire(time.Now())
	if _, ok := reg.Get(hs.Info().ID); !ok {
		t.Fatal("must survive within grace")
	}
	hub.Expire(time.Now().Add(DisconnectGrace + time.Second))
	if _, ok := reg.Get(hs.Info().ID); ok {
		t.Fatal("must be removed after grace")
	}
}
