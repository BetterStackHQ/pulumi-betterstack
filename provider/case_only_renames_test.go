package provider

import (
	"testing"

	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	md "github.com/pulumi/pulumi-terraform-bridge/v3/unstable/metadata"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// A token whose history differs from today's only in its capitals moves to today's token and keeps
// the old one as a past name, so the bridge aliases it instead of keeping a _legacy class. A token
// renamed outright is not a case-only rename and is left to the bridge.
func TestCaseOnlyRenamesBecomePastNames(t *testing.T) {
	info := tfbridge.ProviderInfo{
		MetadataInfo: tfbridge.NewProviderMetadata([]byte(`{"auto-aliasing":{"resources":{
			"betterstack_a_integration":{"current":"betterstack:index/aCloudwatch:ACloudwatch","majorVersion":0},
			"betterstack_b_integration":{"current":"betterstack:index/bOld:BOld","majorVersion":0}}}}`)),
		Resources: map[string]*tfbridge.ResourceInfo{
			"betterstack_a_integration": {Tok: tokens.Type("betterstack:index/aCloudWatch:ACloudWatch")},
			"betterstack_b_integration": {Tok: tokens.Type("betterstack:index/bNew:BNew")},
		},
	}
	recordCaseOnlyRenames(&info)
	hist, _, err := md.Get[map[string]map[string]map[string]any](info.GetMetadata(), "auto-aliasing")
	if err != nil {
		t.Fatal(err)
	}
	a := hist["resources"]["betterstack_a_integration"]
	if a["current"] != "betterstack:index/aCloudWatch:ACloudWatch" {
		t.Fatalf("current = %v", a["current"])
	}
	past, _ := a["past"].([]any)
	if len(past) != 1 || past[0].(map[string]any)["name"] != "betterstack:index/aCloudwatch:ACloudwatch" {
		t.Fatalf("past = %v", a["past"])
	}
	if b := hist["resources"]["betterstack_b_integration"]; b["current"] != "betterstack:index/bOld:BOld" || b["past"] != nil {
		t.Fatalf("an outright rename was touched: %v", b)
	}
}
