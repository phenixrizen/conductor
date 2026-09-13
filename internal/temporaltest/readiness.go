// Package temporaltest provides protocol readiness checks for test-owned local
// Temporal servers. Production processes retain their own connection policy.
package temporaltest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// AwaitReady waits for the actual Temporal frontend and exact configured
// namespace. A listening TCP socket can precede both gRPC and namespace setup.
// These probes perform only service metadata reads, never workflow commands.
// The caller owns the existing startup budget and any child-process supervision.
func AwaitReady(ctx context.Context, address, namespace string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || namespace == "" {
		return errors.New("owned Temporal readiness requires a literal loopback endpoint and namespace")
	}
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		err := probe(attempt, address, namespace)
		cancel()
		if err == nil && ctx.Err() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Provider/SDK error text is deliberately omitted from fixture diagnostics.
	return fmt.Errorf("owned Temporal endpoint and namespace were not ready: %w", ctx.Err())
}

func probe(ctx context.Context, address, namespace string) error {
	c, err := client.DialContext(ctx, client.Options{
		HostPort: address, Namespace: namespace, Identity: "conductor-owned-readiness",
		Logger: quietLogger{}, ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20},
	})
	if err != nil {
		return err
	}
	defer c.Close()
	response, err := c.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{Namespace: namespace})
	if err != nil {
		return err
	}
	info := response.GetNamespaceInfo()
	if info.GetName() != namespace || info.GetId() == "" || info.GetState() != enumspb.NAMESPACE_STATE_REGISTERED {
		return errors.New("configured owned namespace is not registered")
	}
	return nil
}

type quietLogger struct{}

func (quietLogger) Debug(string, ...interface{}) {}
func (quietLogger) Info(string, ...interface{})  {}
func (quietLogger) Warn(string, ...interface{})  {}
func (quietLogger) Error(string, ...interface{}) {}
