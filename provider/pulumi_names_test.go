package provider

import (
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
)

// The plan's messages name a property as the SDKs do, including the bridge's plural list names.
func TestPulumiPropertyPathFollowsTheBridgeNames(t *testing.T) {
	info := Provider()
	name := pulumiPropertyPath(&info)
	for _, c := range []struct {
		resource string
		p        path.Path
		want     string
	}{
		{"betterstack_source", path.Root("data_region"), "dataRegion"},
		{"betterstack_connection", path.Root("ip_allowlist"), "ipAllowlist"},
		{"betterstack_connection", path.Root("ip_allowlist").AtListIndex(0), "ipAllowlist[0]"},
		{"betterstack_errors_slack_integration", path.Root("tag_filter"), "tagFilters"},
		{"betterstack_unknown", path.Root("data_region"), "data_region"},
	} {
		if got := name(c.resource, c.p); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.resource, c.p, got, c.want)
		}
	}
}

// Servers built in parallel share one plan-message hook, and it names properties as the SDKs do even
// while another Provider() call is still building its info.
func TestPlanMessageNamesWhileProvidersAreBuilt(t *testing.T) {
	Provider()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); Provider() }()
		go func() {
			defer wg.Done()
			for range 200 {
				if got := pulumiMessageNames("betterstack_source", path.Root("data_region")); got != "dataRegion" {
					t.Errorf("pulumiMessageNames = %q, want dataRegion", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
