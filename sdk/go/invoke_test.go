package sdk_test

import (
	"sync"
	"testing"

	"github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type mocks struct{}

func (mocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return "42", args.Inputs, nil
}

func (mocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{"pronounceableName": resource.NewStringProperty("read back")}, nil
}

// Programs pass a resource's id, an Output, to a data source. The Pulumi Go SDK this module
// requires must marshal that: v3.246.0 failed with "cannot marshal Output value".
func TestDataSourceTakesAnOutputArgument(t *testing.T) {
	var mu sync.Mutex
	var name string
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		monitor, err := betterstack.NewMonitor(ctx, "monitor", &betterstack.MonitorArgs{MonitorType: pulumi.String("status")})
		if err != nil {
			return err
		}
		read := betterstack.LookupMonitorOutput(ctx, betterstack.LookupMonitorOutputArgs{Id: monitor.ID().ToStringOutput()})
		read.PronounceableName().ApplyT(func(v string) string { mu.Lock(); name = v; mu.Unlock(); return v })
		return nil
	}, pulumi.WithMocks("project", "stack", mocks{}))
	if err != nil {
		t.Fatal(err)
	}
	if name != "read back" {
		t.Fatalf("data source returned %q", name)
	}
}
