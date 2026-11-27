package provider

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A class name the schema spells with a capital inside a word reaches the resource, its get
// function and its enum, including when the title leaves out the name's leading words, and no two
// tokens differ only in case: Pulumi rejects them.
func TestClassNamesFollowTheSchema(t *testing.T) {
	info := Provider()
	res := info.Resources["betterstack_incidents_aws_cloudwatch_integration"]
	if got := res.Tok.String(); got != "betterstack:index/incidentsAwsCloudWatchIntegration:IncidentsAwsCloudWatchIntegration" {
		t.Fatalf("resource token = %s", got)
	}
	if got := info.DataSources["betterstack_incidents_aws_cloudwatch_integration"].Tok.String(); got != "betterstack:index/getIncidentsAwsCloudWatchIntegration:getIncidentsAwsCloudWatchIntegration" {
		t.Fatalf("function token = %s", got)
	}
	if _, ok := info.ExtraTypes["betterstack:index/incidentsOutgoingPagerDutyIntegrationSeverity:IncidentsOutgoingPagerDutyIntegrationSeverity"]; !ok {
		t.Fatalf("the severity enum doesn't follow the class name")
	}
	seen := map[string]string{}
	for tf, r := range info.Resources {
		if strings.HasSuffix(tf, "_legacy") {
			t.Errorf("%s: a legacy class for a rename", tf)
		}
		lower := strings.ToLower(r.Tok.String())
		if other, dup := seen[lower]; dup {
			t.Errorf("%s and %s have tokens that differ only in case", tf, other)
		}
		seen[lower] = tf
	}
	for tf := range info.DataSources {
		if strings.HasSuffix(tf, "_legacy") {
			t.Errorf("%s: a legacy function for a rename", tf)
		}
	}
}

// No nested type may take a class name the SDKs already use. Each SDK derives several classes from a
// resource (R, RArgs, RState, and Go's GetR lookup), a function (F, FArgs, FInvokeArgs, FOutputArgs,
// FResult) and a nested type (T, TArgs, TGetArgs, TResult), and two equal names don't compile in .NET
// (a program that imports both namespaces can't tell DashboardChartArgs from Inputs.DashboardChartArgs)
// and get a renamed Go type.
func TestNoNestedTypeIsNamedLikeAResource(t *testing.T) {
	raw, err := os.ReadFile("cmd/pulumi-resource-betterstack/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Resources map[string]json.RawMessage `json:"resources"`
		Functions map[string]json.RawMessage `json:"functions"`
		Types     map[string]json.RawMessage `json:"types"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	name := func(token string) string { return upperFirst(token[strings.LastIndex(token, ":")+1:]) }
	taken := map[string]string{}
	for token := range schema.Resources {
		for _, suffix := range []string{"", "Args", "State"} {
			taken[name(token)+suffix] = token
		}
		taken["Get"+name(token)] = token
	}
	for token := range schema.Functions {
		for _, suffix := range []string{"", "Args", "InvokeArgs", "OutputArgs", "Result"} {
			taken[name(token)+suffix] = token
		}
	}
	for token := range schema.Types {
		for _, suffix := range []string{"", "Args", "GetArgs", "Result"} {
			if other, clash := taken[name(token)+suffix]; clash {
				t.Errorf("nested type %s makes the class %s, which %s makes too", token, name(token)+suffix, other)
			}
		}
	}
}
