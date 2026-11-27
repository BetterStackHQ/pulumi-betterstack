package provider

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/apitest"
	"github.com/pulumi/providertest/pulumitest/opttest"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
)

// TestUpgrade applies each resource's create fixture with a baseline build of the plugin (the last
// release, or the base branch until there is one), then previews the same program with this build
// against that state. Moving to this build must never replace or delete what users already manage.
//
// PULUMI_BASELINE_PROVIDER names the baseline pulumi-resource-betterstack binary; without it the
// test skips. `make upgrade_test` builds one from a git ref, and CI builds it on every pull request
// that touches the provider (.github/workflows/pulumi-pr.yml).
func TestUpgrade(t *testing.T) {
	baseline := os.Getenv("PULUMI_BASELINE_PROVIDER")
	if baseline == "" {
		t.Skip("PULUMI_BASELINE_PROVIDER is not set")
	}
	requirePulumiCLI(t)
	info := Provider()
	var (
		mu            sync.Mutex
		upgradedNames []string
		skipped       []string
	)
	t.Run("resources", func(t *testing.T) {
		for _, fx := range apitest.Fixtures() {
			t.Run(fx.Resource, func(t *testing.T) {
				t.Parallel()
				res := info.Resources[fx.TFType]
				props, err := pulumiInputs(fx.Resource, fx.Create, info.P.ResourcesMap().Get(fx.TFType).Schema(), res.Fields)
				if err != nil {
					t.Fatal(err)
				}
				program := map[string]cliResource{"subject": {Type: res.Tok.String(), Properties: props}}
				mock := apitest.NewMockAPI()
				t.Cleanup(mock.Close)
				if fx.Seed != nil {
					if err := mock.Seed(fx.Seed.Resource, fx.Seed.Vars, fx.Seed.ID); err != nil {
						t.Fatal(err)
					}
				}
				mock.InjectItemFields(fx.Resource, fx.ComputedItemFields)
				mock.InjectOnCreate(fx.Resource, fx.Settle)

				name := "upgrade-" + strings.ReplaceAll(fx.Resource, "_", "-")
				old := newCLIStackWith(t, name, mock, program, opttest.AttachProviderBinary("betterstack", baseline))
				if _, messages, err := old.up(); err != nil {
					// A resource the baseline does not have, or inputs it does not accept, has no state
					// for users to upgrade from. The schema comparison reports a removed or renamed input.
					mu.Lock()
					skipped = append(skipped, fx.Resource)
					mu.Unlock()
					t.Skipf("the baseline cannot create it, so there is nothing to upgrade: %v\n%s", err, strings.Join(messages, "\n"))
				}
				state := old.pt.ExportStack(t)

				upgraded := newCLIStackWith(t, name, mock, program, thisBuild(t))
				upgraded.pt.ImportStack(t, state)
				if err := expectNoReplaceOrDelete(upgraded); err != nil {
					t.Fatal(err)
				}
				if _, err := upgraded.pt.DestroyErr(t); err != nil {
					t.Fatalf("destroy with this build: %v", err)
				}
				mu.Lock()
				upgradedNames = append(upgradedNames, fx.Resource)
				mu.Unlock()
			})
		}
	})
	sort.Strings(skipped)
	// Printed rather than logged so it shows without -v, in CI's log and its job summary.
	summary := fmt.Sprintf("Upgrade test: %d resources upgraded with no replacement or deletion; %d skipped because the baseline cannot create them%s\n",
		len(upgradedNames), len(skipped), listed(skipped))
	fmt.Print(summary)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			_, _ = f.WriteString("\n" + summary)
			_ = f.Close()
		}
	}
	// A baseline that creates nothing makes every subtest skip and the check pass while testing
	// nothing: that is a broken baseline, not a clean upgrade.
	if len(upgradedNames) == 0 && !t.Failed() {
		t.Fatal("no resource could be created with the baseline, so nothing was upgraded")
	}
}

func listed(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return ": " + strings.Join(names, ", ")
}

// expectNoReplaceOrDelete previews the stack and fails on any replacement or deletion; an in-place
// update is allowed (a new default, a renamed computed value), and is logged.
func expectNoReplaceOrDelete(s *cliStack) error {
	collect, done := diagnostics()
	result, err := s.pt.PreviewErr(s.t, optpreview.Diff(), optpreview.EventStreams(collect))
	messages := done()
	if err != nil {
		return fmt.Errorf("preview with this build: %w\n%s", err, result.StdOut)
	}
	ops := opCounts(result.ChangeSummary)
	for _, op := range []string{"replace", "create-replacement", "delete-replaced", "delete"} {
		if ops[op] > 0 {
			return fmt.Errorf("upgrading plans %v:\n%s\n%s", ops, strings.Join(messages, "\n"), result.StdOut)
		}
	}
	if ops["update"] > 0 {
		s.t.Logf("upgrading plans an in-place update:\n%s", result.StdOut)
	}
	return nil
}
