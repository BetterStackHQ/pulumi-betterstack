package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// An input that takes another resource's id is a string, as every resource id is, so a program passes
// group.id without a conversion. The Terraform provider holds ids as strings, so this covers nested
// ids and data sources too. A teamId that is a number is the team's own number, which some APIs take
// instead of an id such as t123.
func TestReferenceInputsAreStrings(t *testing.T) {
	schema := generatedSchema(t)
	seen := 0
	for token, raw := range schema.Resources {
		var res struct {
			InputProperties map[string]struct {
				Type string `json:"type"`
			} `json:"inputProperties"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		for name, p := range res.Properties {
			if strings.HasSuffix(name, "Id") && name != "teamId" && p.Type == "integer" {
				t.Errorf("output %s.%s is an integer, want a string", token, name)
			}
		}
		for name, p := range res.InputProperties {
			if !strings.HasSuffix(name, "Id") || name == "teamId" {
				continue
			}
			seen++
			if p.Type == "integer" {
				t.Errorf("%s.%s is an integer, want a string like the id it references", token, name)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no id inputs found")
	}
}
