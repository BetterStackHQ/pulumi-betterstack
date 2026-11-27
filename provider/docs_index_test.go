package provider

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/betterstackhq/terraform-provider-betterstack/pkg/apitest"
)

// The Registry overview page (docs/_index.md) is the first program most users copy. Its YAML
// example runs through the Pulumi CLI against the mock API, so it cannot name a resource or an
// input the provider does not have, and must converge to an empty preview.
func TestDocsIndexYAMLExampleRuns(t *testing.T) {
	requirePulumiCLI(t)
	raw, err := os.ReadFile("../docs/_index.md")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)\\{\\{% choosable language yaml %\\}\\}\\s*```yaml\\n(.*?)```").FindSubmatch(raw)
	if block == nil {
		t.Fatal("docs/_index.md has no YAML example under the yaml chooser tab")
	}
	program := string(block[1])
	if !strings.HasPrefix(program, "name: ") || !strings.Contains(program, "runtime: yaml") {
		t.Fatalf("the YAML example is not a complete Pulumi.yaml program:\n%s", program)
	}
	mock := apitest.NewMockAPI()
	t.Cleanup(mock.Close)
	stack := newCLIStackFromProgram(t, "docs-index", mock, program, thisBuild(t))
	if _, messages, err := stack.up(); err != nil {
		t.Fatalf("up: %v\n%s", err, strings.Join(messages, "\n"))
	}
	if err := stack.expectEmptyPreview("the docs example"); err != nil {
		t.Fatal(err)
	}
	// The example's monitor starts protected, so a plain destroy is refused. Turning protection off
	// first, as a user would, lets the destroy that cleans up the stack go through.
	if _, err := stack.pt.DestroyErr(t); err == nil {
		t.Fatal("destroy of the example's monitor went through, but deletion protection is on by default")
	}
	unprotected := strings.Replace(program, "      monitorType: status\n", "      monitorType: status\n      deletionProtection: false\n", 1)
	if unprotected == program {
		t.Fatalf("the YAML example has no monitorType line to add deletionProtection after:\n%s", program)
	}
	stack.pt.WritePulumiYaml(t, unprotected)
	if _, messages, err := stack.up(); err != nil {
		t.Fatalf("up with deletionProtection: false: %v\n%s", err, strings.Join(messages, "\n"))
	}
	if _, err := stack.pt.DestroyErr(t); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}
