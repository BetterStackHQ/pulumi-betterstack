// Preserve created resource identity when the upstream bridge discards Terraform state on error
// diagnostics. A failed follow-up PATCH must not make Pulumi orphan the successful POST.
package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/rpcutil/rpcerror"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/grpc/status"
)

// LogSink is the bridge's logging sink, which lives in an internal package and so cannot be named
// from here; *github.com/pulumi/pulumi/pkg/v3/resource/provider.HostClient implements it. A nil
// LogSink means "no host to log to", which is what the tests pass.
type LogSink interface {
	Log(ctx context.Context, sev diag.Severity, urn resource.URN, msg string) error
	LogStatus(ctx context.Context, sev diag.Severity, urn resource.URN, msg string) error
}

// NewServer builds the provider's gRPC server: the bridged Terraform provider plus partial-create
// recovery. The plugin binary and the lifecycle tests both go through here, so the tests drive the
// same server the engine does.
func NewServer(ctx context.Context, sink LogSink, meta pftfbridge.ProviderMetadata) (pulumirpc.ResourceProviderServer, error) {
	info := Provider()
	shim, ok := info.P.(pf.ShimProvider)
	if !ok {
		return nil, fmt.Errorf("provider shim %T cannot serve the plugin-framework runtime", info.P)
	}
	// Only the runtime provider gets the wrapped shim. tfgen keeps the plain one, so schema and
	// bridge-metadata generation see exactly the provider they saw before.
	info.P = capturingShim{shim}
	applyRuntimeSingularLists(&info)

	srv, err := pftfbridge.NewProviderServer(ctx, sink, info, meta)
	if err != nil {
		return nil, err
	}
	return recoveringServer{srv}, nil
}

// createdIDKey addresses the createdID slot carried by a single Create RPC's context.
type createdIDKey struct{}

// createdID carries the id of a partially created object from the Terraform layer back to the
// Pulumi Create call that started it. One slot per Create RPC, reached through that call's context,
// so concurrent creates can never observe each other's ids.
type createdID struct {
	mu sync.Mutex
	id string
}

func (c *createdID) record(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id == "" {
		c.id = id
	}
}

func (c *createdID) value() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id
}

// capturingShim is the schema shim with a Terraform server that reports partially created ids.
type capturingShim struct {
	pf.ShimProvider
}

func (s capturingShim) Server(ctx context.Context) (tfprotov6.ProviderServer, error) {
	srv, err := s.ShimProvider.Server(ctx)
	if err != nil {
		return nil, err
	}
	return capturingServer{srv}, nil
}

type capturingServer struct {
	tfprotov6.ProviderServer
}

// ApplyResourceChange notes the id of an object a failed create left behind. Updates and deletes
// pass straight through: only Create installs a slot in the context.
func (c capturingServer) ApplyResourceChange(
	ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest,
) (*tfprotov6.ApplyResourceChangeResponse, error) {
	resp, err := c.ProviderServer.ApplyResourceChange(ctx, req)
	if err != nil || resp == nil || resp.NewState == nil {
		return resp, err
	}
	created, ok := ctx.Value(createdIDKey{}).(*createdID)
	if !ok || !hasError(resp.Diagnostics) {
		return resp, nil
	}
	if id, ok := c.stateID(ctx, req.TypeName, resp.NewState); ok {
		created.record(id)
	}
	return resp, nil
}

func hasError(diagnostics []*tfprotov6.Diagnostic) bool {
	for _, d := range diagnostics {
		if d != nil && d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

// stateID reads the "id" attribute out of the state an errored apply still produced. The provider
// schema is fetched rather than cached on purpose: this runs only on the failure path.
func (c capturingServer) stateID(
	ctx context.Context, typeName string, state *tfprotov6.DynamicValue,
) (string, bool) {
	schemas, err := c.ProviderServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || schemas == nil {
		return "", false
	}
	schema, ok := schemas.ResourceSchemas[typeName]
	if !ok || schema == nil {
		return "", false
	}
	value, err := state.Unmarshal(schema.ValueType())
	if err != nil || !value.IsKnown() || value.IsNull() {
		return "", false
	}
	var attrs map[string]tftypes.Value
	if err := value.As(&attrs); err != nil {
		return "", false
	}
	attr, ok := attrs["id"]
	if !ok || !attr.IsKnown() || attr.IsNull() || !attr.Type().Is(tftypes.String) {
		return "", false
	}
	var id string
	if err := attr.As(&id); err != nil {
		return "", false
	}
	return id, id != ""
}

// recoveringServer is the bridged provider's gRPC surface with create recovery layered on top.
type recoveringServer struct {
	pulumirpc.ResourceProviderServer
}

func (r recoveringServer) Create(
	ctx context.Context, req *pulumirpc.CreateRequest,
) (*pulumirpc.CreateResponse, error) {
	created := &createdID{}
	resp, err := r.ResourceProviderServer.Create(context.WithValue(ctx, createdIDKey{}, created), req)
	if err == nil {
		return resp, nil
	}
	id := created.value()
	if id == "" {
		// Nothing was created (or the state carried no id): an ordinary create failure.
		return nil, err
	}

	// Recover live values without presenting the rejected plan as observed state.
	read, readErr := r.ResourceProviderServer.Read(ctx, &pulumirpc.ReadRequest{Urn: req.GetUrn(), Id: id})
	st := status.Convert(err)
	failure := &pulumirpc.ErrorResourceInitFailed{Id: id, Reasons: []string{st.Message()}}
	if readErr == nil && read.GetId() != "" {
		failure.Properties = read.GetProperties()
	} else if readErr != nil {
		// A failed read must not discard the identity the create already allocated.
		failure.Reasons = append(failure.Reasons, "Reading partially created resource: "+readErr.Error())
	}
	return nil, rpcerror.WithDetails(rpcerror.New(st.Code(), st.Message()), failure)
}
