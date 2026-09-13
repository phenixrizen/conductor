package contextworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

type targetFixtureClient struct {
	client.Client
	service *targetFixtureService
}

func (c *targetFixtureClient) WorkflowService() workflowservicepb.WorkflowServiceClient {
	return c.service
}

type targetFixtureService struct {
	workflowservicepb.WorkflowServiceClient
	cluster   *workflowservicepb.GetClusterInfoResponse
	namespace *workflowservicepb.DescribeNamespaceResponse
	err       error
}

func (s *targetFixtureService) GetClusterInfo(context.Context, *workflowservicepb.GetClusterInfoRequest, ...grpc.CallOption) (*workflowservicepb.GetClusterInfoResponse, error) {
	return s.cluster, s.err
}
func (s *targetFixtureService) DescribeNamespace(context.Context, *workflowservicepb.DescribeNamespaceRequest, ...grpc.CallOption) (*workflowservicepb.DescribeNamespaceResponse, error) {
	return s.namespace, s.err
}

func targetFixture() *targetFixtureClient {
	return &targetFixtureClient{service: &targetFixtureService{
		cluster: &workflowservicepb.GetClusterInfoResponse{ClusterId: "cluster-id-one"},
		namespace: &workflowservicepb.DescribeNamespaceResponse{
			NamespaceInfo: &namespacepb.NamespaceInfo{Name: "namespace-a", Id: "namespace-id-one", State: enumspb.NAMESPACE_STATE_REGISTERED},
			Config:        &namespacepb.NamespaceConfig{WorkflowExecutionRetentionTtl: durationpb.New(24 * time.Hour)},
		},
	}}
}

func TestRuntimeTargetIncludesActualExecutionIdentity(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*targetFixtureClient)
	}{
		{"cluster recreated", func(c *targetFixtureClient) { c.service.cluster.ClusterId = "cluster-id-two" }},
		{"namespace recreated", func(c *targetFixtureClient) { c.service.namespace.NamespaceInfo.Id = "namespace-id-two" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			c := targetFixture()
			before, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", TaskQueue)
			if err != nil || !digest.MatchString(before) {
				t.Fatalf("target: %q %v", before, err)
			}
			change.mutate(c)
			after, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", TaskQueue)
			if err != nil || after == before {
				t.Fatalf("changed identity retained target: %q %v", after, err)
			}
		})
	}
	c := targetFixture()
	one, err := RuntimeTarget(context.Background(), c, "namespace-a", "LOCALHOST:07233", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	two, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", TaskQueue)
	if err != nil || one != two {
		t.Fatalf("equivalent addresses changed target: %v", err)
	}
	three, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", "other-queue")
	if err != nil || two == three {
		t.Fatalf("task queue was not bound: %v", err)
	}
}

func TestRuntimeTargetFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*targetFixtureClient)
		want   error
	}{
		{"unknown cluster", func(c *targetFixtureClient) { c.service.cluster.ClusterId = "" }, ErrInvalidConfiguration},
		{"unknown namespace", func(c *targetFixtureClient) { c.service.namespace.NamespaceInfo.Id = "" }, ErrInvalidConfiguration},
		{"wrong namespace", func(c *targetFixtureClient) { c.service.namespace.NamespaceInfo.Name = "namespace-b" }, ErrInvalidConfiguration},
		{"deprecated namespace", func(c *targetFixtureClient) {
			c.service.namespace.NamespaceInfo.State = enumspb.NAMESPACE_STATE_DEPRECATED
		}, ErrInvalidConfiguration},
		{"short retention", func(c *targetFixtureClient) {
			c.service.namespace.Config.WorkflowExecutionRetentionTtl = durationpb.New(time.Hour)
		}, ErrInvalidConfiguration},
		{"missing retention", func(c *targetFixtureClient) { c.service.namespace.Config.WorkflowExecutionRetentionTtl = nil }, ErrInvalidConfiguration},
		{"network error", func(c *targetFixtureClient) { c.service.err = errors.New("private upstream diagnostic") }, ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := targetFixture()
			test.mutate(c)
			if _, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", TaskQueue); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestBoundRuntimeRejectsChangedTargetBeforeExecutionRPC(t *testing.T) {
	c := targetFixture()
	target, err := RuntimeTarget(context.Background(), c, "namespace-a", "localhost:7233", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewBoundRuntime(c, "namespace-a", "localhost:7233", TaskQueue, target)
	if err != nil {
		t.Fatal(err)
	}
	c.service.cluster.ClusterId = "replacement-cluster"
	// The fixture intentionally has no execution RPC implementations: reaching
	// one after the identity mismatch would panic and fail this test.
	id := WorkflowName + "/" + testReference.ID
	if _, err = r.Start(context.Background(), id, testReference); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("start after target change: %v", err)
	}
	if _, err = r.Lookup(context.Background(), id, "", testReference); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("lookup after target change: %v", err)
	}
	if err = r.Cancel(context.Background(), id, "", testReference); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cancel after target change: %v", err)
	}
}
