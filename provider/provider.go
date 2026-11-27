// Package provider builds the Pulumi bridge metadata for the Better Stack provider by wrapping the
// schema-generated Terraform plugin-framework provider with pulumi-terraform-bridge. Resource and
// data-source tokens are auto-derived from the `betterstack_` Terraform names, so adding a resource
// to the unified-api schema needs no change here — regenerate the TF provider and the token shows up.
package provider

import (
	_ "embed"
	"sync"
	"sync/atomic"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/shim"
	"github.com/hashicorp/terraform-plugin-framework/path"
	pfprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
)

// bridgeMetadata is the KV store persisted by tfgen alongside schema.json; the resource plugin reads
// it at startup. It is overwritten by `pulumi-tfgen-betterstack`; the committed file is the source.
//
//go:embed cmd/pulumi-resource-betterstack/bridge-metadata.json
var bridgeMetadata []byte

// Version is overridden at build time via -ldflags; the default keeps `go test` and local tfgen valid.
var Version = "0.1.0"

// The provider's plan messages name properties the way the SDKs do, through the first fully built
// ProviderInfo. The process-wide hooks (those names and the Pulumi description edit) are installed
// once, and the names never come from an info under construction: tests start several servers in
// parallel, each calling Provider().
var (
	builtInfo       atomic.Pointer[tfbridge.ProviderInfo]
	installMessages sync.Once
)

func pulumiMessageNames(resourceType string, p path.Path) string {
	info := builtInfo.Load()
	if info == nil {
		return p.String()
	}
	return pulumiPropertyPath(info)(resourceType, p)
}

func terraformProvider() pfprovider.Provider {
	var p pfprovider.Provider
	installMessages.Do(func() {
		shim.SetPulumiDescriptionEdit(pulumiText)
		shim.SetPulumiDeletesFirst(shim.DeletesFirst())
		p = shim.NewForPulumi("pulumi-betterstack", Version, pulumiMessageNames)
	})
	if p == nil {
		p = shim.New("pulumi-betterstack", Version)
	}
	return p
}

// Provider returns the bridged provider metadata consumed by tfgen (schema) and the resource plugin.
func Provider() tfbridge.ProviderInfo {
	upstreamLicense := tfbridge.Apache20LicenseType
	info := tfbridge.ProviderInfo{
		P:                 pftfbridge.ShimProvider(terraformProvider()),
		Name:              "betterstack",
		Version:           Version,
		DisplayName:       "Better Stack",
		Publisher:         "Better Stack",
		GitHubOrg:         "betterstackhq",
		Repository:        "https://github.com/BetterStackHQ/pulumi-betterstack",
		Homepage:          "https://github.com/BetterStackHQ/pulumi-betterstack",                                      // npm, PyPI and NuGet's project URL
		Description:       "A Pulumi package for managing errors, incidents and telemetry resources in Better Stack.", // one product, never a sub-product (DESCRIPTIONS.md)
		License:           "Apache-2.0",
		TFProviderLicense: &upstreamLicense,
		Keywords:          keywords(),
		// The plugin binaries are attached to the vendor repository's GitHub releases, so `pulumi up`
		// installs the plugin by itself. The Pulumi Registry shows the logo and asks for an SVG
		// wordmark: assets/logo.svg, served from the vendor repository. The NuGet icon must be PNG or
		// JPEG, so the Makefile replaces what SDK generation downloads from here with assets/logo.png.
		PluginDownloadURL: "github://api.github.com/BetterStackHQ/pulumi-betterstack",
		LogoURL:           "https://raw.githubusercontent.com/BetterStackHQ/pulumi-betterstack/main/assets/logo.svg",
		// The generated TF provider tree (relative to tfgen's cwd, provider/): its registry docs
		// become the Pulumi resource/function descriptions and import sections.
		UpstreamRepoPath: "../upstream",
		MetadataInfo:     tfbridge.NewProviderMetadata(bridgeMetadata),
		Golang: &tfbridge.GolangInfo{
			ImportBasePath:                 "github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack",
			GenerateResourceContainerTypes: true,
			RespectSchemaVersion:           true,
		},
		JavaScript: &tfbridge.JavaScriptInfo{
			PackageName:          "@better_stack/pulumi",
			Dependencies:         map[string]string{"@pulumi/pulumi": "^3.0.0"},
			RespectSchemaVersion: true,
		},
		Python: &tfbridge.PythonInfo{
			PackageName:          "pulumi_betterstack",
			Requires:             map[string]string{"pulumi": ">=3.0.0,<4.0.0"},
			RespectSchemaVersion: true,
		},
		// The .NET namespace is Pulumi.BetterStack, as for Pulumi's own providers (the NuGet package ID is
		// BetterStack.Pulumi, set by the Makefile, since nuget.org reserves Pulumi.* for Pulumi). A root
		// namespace of BetterStack would make BetterStack.BetterStack, whose inner BetterStack hides the
		// outer one, so the SDK's fully qualified enum types don't compile. Namespaces keeps the
		// capital S; without it the generator title-cases the package name into Betterstack.
		CSharp: &tfbridge.CSharpInfo{
			RootNamespace:        "Pulumi",
			Namespaces:           map[string]string{"betterstack": "BetterStack"},
			PackageReferences:    map[string]string{"Pulumi": "3.*"},
			RespectSchemaVersion: true,
		},
		Java: &tfbridge.JavaInfo{
			BasePackage:                     "com.betterstack",
			BuildFiles:                      "gradle",
			GradleNexusPublishPluginVersion: "2.0.0",
			Packages:                        map[string]string{"betterstack": "pulumi"},
		},
	}

	info.DocRules = &tfbridge.DocRuleInfo{
		EditRules: func(defaults []tfbridge.DocsEdit) []tfbridge.DocsEdit {
			return append(defaults, schemaSectionEdit(), terraformTextEdit(), apiValueEdit(info.P))
		},
	}

	err := tokens.ComputeTokens(&info, tokens.SingleModule(
		"betterstack_", "index", tokens.MakeStandard("betterstack"),
	))
	contract.AssertNoErrorf(err, "computing Pulumi resource tokens from the Terraform schema")
	applyClassNames(&info)
	applyIDInputs(&info)
	applyEnums(&info)
	applySingularLists(&info)
	applyNestedTypeNames(&info)
	for _, name := range shim.DeletesFirst() {
		if res := info.Resources[name]; res != nil {
			res.DeleteBeforeReplace = true
		}
	}

	// Records every token and field name in bridge-metadata.json, so a later rename (a resource,
	// a nested type, a list that becomes a single object) moves users' stacks instead of breaking
	// them. The history only covers renames made after it starts, so it starts before the first release.
	recordCaseOnlyRenames(&info)
	info.MustApplyAutoAliases()

	built := info
	builtInfo.CompareAndSwap(nil, &built)
	return info
}

// keywords are the package's search terms: what people look for, not the names of Better Stack's
// APIs, since Better Stack is one product. category/monitoring is the Pulumi Registry's category.
func keywords() []string {
	return []string{"betterstack", "observability", "monitoring", "uptime", "incidents", "on-call",
		"status-page", "logs", "metrics", "errors", "category/monitoring"}
}
