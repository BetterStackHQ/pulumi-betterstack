package provider

import (
	"regexp"
	"strings"

	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
)

// schemaSectionEdit moves the prose of a docs page's "## Schema" section above it. The bridge reads
// that section's attribute lists only when every heading is followed directly by its list, and our
// pages open the section, and its Optional list, with a sentence ("At least one of url or
// scenario_name must be configured", "Optional attributes are also computed"), and a nested
// resource lists its parent under an "### Address" heading the bridge doesn't know. It then parsed
// no attribute docs at all, so the SDKs fell back to the bare schema descriptions without their valid
// values, defaults and create-only notes. Moved up, the sentences stay in the resource's description.
func schemaSectionEdit() tfbridge.DocsEdit {
	return tfbridge.DocsEdit{
		Path: "*",
		Edit: func(_ string, content []byte) ([]byte, error) { return []byte(liftSchemaProse(string(content))), nil },
	}
}

func liftSchemaProse(md string) string {
	lines := strings.Split(md, "\n")
	start := -1
	for i, l := range lines {
		if l == "## Schema" {
			start = i
			break
		}
	}
	if start < 0 {
		return md
	}
	var kept, prose []string
	i := start + 1
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		l := lines[i]
		switch {
		// A nested section's "Required:", "Optional:" and "Read-Only:" labels stay: the bridge reads a
		// nested field's text only under one.
		case l == "", strings.HasPrefix(l, "#"), strings.HasPrefix(l, "- "), strings.HasPrefix(l, "  "), strings.HasPrefix(l, "<a id="),
			l == "Required:", l == "Optional:", l == "Read-Only:":
			kept = append(kept, l)
		default:
			prose = append(prose, l, "")
		}
	}
	kept = foldAddressIntoRequired(flattenTypeLabels(foldSubItems(kept)))
	out := append([]string{}, lines[:start]...)
	out = append(out, prose...)
	out = append(out, lines[start])
	out = append(out, kept...)
	out = append(out, lines[i:]...)
	return strings.Join(out, "\n")
}

// foldAddressIntoRequired moves the parent attributes listed under "### Address" into the Required
// list: they are required inputs, and Address is not a heading the bridge reads.
func foldAddressIntoRequired(section []string) []string {
	var address, rest []string
	inAddress := false
	for _, l := range section {
		switch {
		case l == "### Address":
			inAddress = true
		case strings.HasPrefix(l, "#"):
			inAddress = false
			rest = append(rest, l)
		case inAddress:
			if strings.HasPrefix(l, "- ") {
				address = append(address, l)
			}
		default:
			rest = append(rest, l)
		}
	}
	if len(address) == 0 {
		return section
	}
	for i, l := range rest {
		if l == "### Required" {
			out := append(append([]string{}, rest[:i+1]...), "")
			out = append(out, address...)
			return append(out, rest[i+1:]...)
		}
	}
	return append(append([]string{"### Required", ""}, address...), rest...)
}

// foldSubItems joins the sub-bullets under an attribute (each value of an enum with its meaning) onto
// the attribute's own line: the bridge reads a bullet only when it holds one paragraph, and one that
// can't be read drops the whole page's attribute docs.
func foldSubItems(section []string) []string {
	var out []string
	for _, l := range section {
		if strings.HasPrefix(l, "  - ") && len(out) > 0 && strings.HasPrefix(out[len(out)-1], "- ") {
			out[len(out)-1] += " " + strings.TrimPrefix(l, "  - ")
			continue
		}
		out = append(out, l)
	}
	return out
}

// A type with a note in parentheses, (String (RFC 3339)), reads as (String, RFC 3339): the bridge ends
// the type at its first closing parenthesis and would start the description with the rest.
var nestedTypeLabel = regexp.MustCompile("^(- `[^`]+` [(])([^()]*) [(]([^()]*)[)]([^()]*)[)]")

func flattenTypeLabels(section []string) []string {
	out := make([]string, len(section))
	for i, l := range section {
		out[i] = nestedTypeLabel.ReplaceAllString(l, "${1}${2}, ${3}${4})")
	}
	return out
}
