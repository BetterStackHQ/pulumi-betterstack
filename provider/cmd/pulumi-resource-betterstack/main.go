// pulumi-resource-betterstack is the Pulumi provider plugin for Better Stack. It serves the bridged
// Terraform plugin-framework provider against the embedded Pulumi Package Schema.
//
// Serving is spelled out here instead of calling pf/tfbridge.Main, because Main builds the gRPC
// server itself and this provider needs that server wrapped for partial-create recovery (see
// provider/partial_create.go). The flags Main handles are handled below.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	prov "github.com/betterstackhq/pulumi-betterstack/provider"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	rprovider "github.com/pulumi/pulumi/pkg/v3/resource/provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

//go:embed schema.json
var schema []byte

func main() {
	handleFlags()

	ctx := context.Background()
	meta := pftfbridge.ProviderMetadata{PackageSchema: schema}
	err := rprovider.Main("betterstack", func(host *rprovider.HostClient) (pulumirpc.ResourceProviderServer, error) {
		return prov.NewServer(ctx, host, meta)
	})
	if err != nil {
		cmdutil.ExitError(err.Error())
	}
}

// handleFlags serves the two informational flags a bridged plugin is expected to answer. Parsing
// uses its own flag set and ignores errors so the engine's flags (-tracing, -logtostderr, ...),
// which the serving path parses later, do not trip it up.
func handleFlags() {
	flags := flag.NewFlagSet("tf-provider-flags", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dumpInfo := flags.Bool("get-provider-info", false, "dump provider info as JSON to stdout")
	version := flags.Bool("version", false, "get built provider version")
	contract.IgnoreError(flags.Parse(os.Args[1:]))

	if *dumpInfo {
		info := prov.Provider()
		if err := json.NewEncoder(os.Stdout).Encode(tfbridge.MarshalProviderInfo(&info)); err != nil {
			cmdutil.ExitError(err.Error())
		}
		os.Exit(0)
	}
	if *version {
		fmt.Println(prov.Version)
		os.Exit(0)
	}
}
