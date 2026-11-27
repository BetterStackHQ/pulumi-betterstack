// pulumi-tfgen-betterstack generates the Pulumi Package Schema (schema.json) from the bridged
// Terraform provider. Run it as `pulumi-tfgen-betterstack schema --out <dir>`.
package main

import (
	prov "github.com/betterstackhq/pulumi-betterstack/provider"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfgen"
)

func main() {
	tfgen.Main("betterstack", prov.Provider())
}
