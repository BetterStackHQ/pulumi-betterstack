package provider

import (
	"strings"
	"testing"
)

// The bridge reads a docs page's attribute lists only when each heading is followed by its list.
func TestLiftSchemaProse(t *testing.T) {
	in := strings.Join([]string{
		"Intro.", "", "## Schema", "", "At least one of `url` or `name` must be configured.", "",
		"### Address", "", "This object lives under a parent collection.", "", "- `chart_id` (Number) Chart.", "",
		"### Required", "", "- `name` (String) Name.", "",
		"### Optional", "", "Optional attributes are also computed.", "", "- `paused` (Boolean) Paused.", "",
		"<a id=\"nestedatt--bucket\"></a>", "### Nested Schema for `bucket`", "", "Optional:", "", "- `name` (String) Bucket name.", "",
		"Read-Only:", "", "- `id` (String) Bucket ID.", "- `kind` (String) The kind.", "  - `a`: First.", "  - `b`: Second.", "- `created_at` (String (RFC 3339)) When it was created.", "- `valid_until` (String (RFC 3339), Forces replacement) When it expires.", "",
		"## Endpoints", "", "- `GET /x`",
	}, "\n")
	got := liftSchemaProse(in)
	for _, want := range []string{
		"Intro.\n\nAt least one of `url` or `name` must be configured.\n\nThis object lives under a parent collection.\n\nOptional attributes are also computed.\n\n## Schema",
		"### Required\n\n- `chart_id` (Number) Chart.\n\n- `name` (String) Name.",
		"### Optional\n\n\n- `paused` (Boolean) Paused.",
		// The bridge reads a nested field's text only under its label, so the labels stay.
		"### Nested Schema for `bucket`\n\nOptional:\n\n- `name` (String) Bucket name.\n\nRead-Only:\n\n- `id` (String) Bucket ID.",
		// Each value's meaning joins its attribute's line, which the bridge can read.
		"- `kind` (String) The kind. `a`: First. `b`: Second.",
		// The bridge ends a type at its first closing parenthesis.
		"- `created_at` (String, RFC 3339) When it was created.",
		"- `valid_until` (String, RFC 3339, Forces replacement) When it expires.",
		"## Endpoints",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("liftSchemaProse output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "### Address") {
		t.Errorf("the Address heading must go:\n%s", got)
	}
}
