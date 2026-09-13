package temporaltest

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type startingFrontend struct {
	workflowservice.UnimplementedWorkflowServiceServer
	stage atomic.Int32
	seen  chan int32
}

func (s *startingFrontend) observed(stage int32) {
	select {
	case s.seen <- stage:
	default:
	}
}

func (s *startingFrontend) GetSystemInfo(context.Context, *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	if s.stage.Load() == 0 {
		s.observed(0)
		return nil, status.Error(codes.Unavailable, "synthetic-private-startup-details")
	}
	return &workflowservice.GetSystemInfoResponse{}, nil
}

func (s *startingFrontend) DescribeNamespace(_ context.Context, request *workflowservice.DescribeNamespaceRequest) (*workflowservice.DescribeNamespaceResponse, error) {
	stage := s.stage.Load()
	s.observed(stage)
	if stage == 1 {
		return nil, status.Error(codes.NotFound, "namespace initialization pending")
	}
	info := &namespacepb.NamespaceInfo{Name: request.Namespace, Id: "owned-namespace-id", State: enumspb.NAMESPACE_STATE_REGISTERED}
	if stage == 2 {
		info.Name = "different-namespace"
	}
	if stage == 3 {
		info.State = enumspb.NAMESPACE_STATE_DEPRECATED
	}
	return &workflowservice.DescribeNamespaceResponse{NamespaceInfo: info}, nil
}

func startFrontend(t *testing.T) (string, *startingFrontend) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	frontend := &startingFrontend{seen: make(chan int32, 32)}
	server := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(server, frontend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String(), frontend
}

func observeStage(t *testing.T, s *startingFrontend, want int32) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-s.seen:
			if got == want {
				return
			}
		case <-deadline.C:
			t.Fatalf("readiness did not inspect protocol stage %d", want)
		}
	}
}

func TestReadinessRequiresFrontendAndExactRegisteredNamespace(t *testing.T) {
	address, frontend := startFrontend(t)
	// This is precisely the old fixture check: TCP succeeds before the frontend
	// accepts metadata RPCs, so it cannot authorize starting a workflow helper.
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- AwaitReady(ctx, address, "owned-namespace") }()
	for stage := int32(0); stage <= 3; stage++ {
		frontend.stage.Store(stage)
		observeStage(t, frontend, stage)
		select {
		case err := <-ready:
			t.Fatalf("readiness returned before valid namespace at stage %d: %v", stage, err)
		case <-time.After(150 * time.Millisecond):
		}
	}
	frontend.stage.Store(4)
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("ready namespace was not observed")
	}
}

func TestReadinessCancellationAndDeadlineStayBounded(t *testing.T) {
	address, frontend := startFrontend(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)
	go func() { ready <- AwaitReady(ctx, address, "owned-namespace") }()
	observeStage(t, frontend, 0)
	cancel()
	select {
	case err := <-ready:
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private") {
			t.Fatalf("cancellation lost its type or exposed transport details: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness ignored cancellation")
	}
	deadline, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if err := AwaitReady(deadline, address, "owned-namespace"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("readiness changed the caller's startup deadline: %v", err)
	}
}
