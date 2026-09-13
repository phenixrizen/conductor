package contextworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/proto"
)

// NewBoundRuntime protects each execution RPC with a fresh check of the target
// identity persisted by the dispatch transaction. Use this for durable dispatch;
// NewRuntime alone checks execution type/input but not server replacement.
func NewBoundRuntime(c client.Client, namespace, address, taskQueue, target string) (*Runtime, error) {
	r, err := NewRuntime(c, namespace, taskQueue)
	if err != nil || !digest.MatchString(target) {
		return nil, ErrInvalidConfiguration
	}
	r.address, r.target = address, target
	return r, nil
}

func (r *Runtime) checkTarget(ctx context.Context) error {
	if r.target == "" {
		return nil
	}
	actual, err := RuntimeTarget(ctx, r.client, r.namespace, r.address, r.taskQueue)
	if err != nil {
		return err
	}
	if actual != r.target {
		return ErrBindingMismatch
	}
	return nil
}

// RuntimeTarget resolves the actual cluster and namespace identity, rather than
// assuming an unchanged address still names the same retained workflow history.
// Refresh it before each dispatcher/reconciliation batch; startup-only caching
// cannot detect a server recreated while a worker remains connected.
func RuntimeTarget(ctx context.Context, c client.Client, namespace, address, taskQueue string) (string, error) {
	if c == nil || !configurationName.MatchString(namespace) || !configurationName.MatchString(taskQueue) {
		return "", ErrInvalidConfiguration
	}
	host, port, err := net.SplitHostPort(address)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || host == "" || len(host) > 253 || strings.ContainsAny(host, "/@?#\\ \t\r\n") {
		return "", ErrInvalidConfiguration
	}
	address = net.JoinHostPort(strings.ToLower(host), strconv.Itoa(number))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cluster, err := c.WorkflowService().GetClusterInfo(ctx, &workflowservicepb.GetClusterInfoRequest{})
	if err != nil {
		return "", classifyRPC(err)
	}
	if cluster == nil || proto.Size(cluster) > 64*1024 || !configurationName.MatchString(cluster.GetClusterId()) {
		return "", ErrInvalidConfiguration
	}
	description, err := c.WorkflowService().DescribeNamespace(ctx, &workflowservicepb.DescribeNamespaceRequest{Namespace: namespace})
	if err != nil {
		return "", classifyRPC(err)
	}
	if description == nil || proto.Size(description) > 64*1024 || description.GetNamespaceInfo().GetName() != namespace ||
		description.GetNamespaceInfo().GetState() != enumspb.NAMESPACE_STATE_REGISTERED || !configurationName.MatchString(description.GetNamespaceInfo().GetId()) {
		return "", ErrInvalidConfiguration
	}
	retention := description.GetConfig().GetWorkflowExecutionRetentionTtl()
	if retention == nil || retention.CheckValid() != nil || retention.AsDuration() < 24*time.Hour {
		return "", ErrInvalidConfiguration
	}
	// No owner email, description, capabilities, or provider credentials enter
	// the binding. Its opaque digest is a persistence guard, not a secret token.
	data, err := json.Marshal(struct {
		Version                                               int
		ClusterID, NamespaceID, Namespace, Address, TaskQueue string
	}{1, cluster.GetClusterId(), description.GetNamespaceInfo().GetId(), namespace, address, taskQueue})
	if err != nil {
		return "", ErrInvalidConfiguration
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
