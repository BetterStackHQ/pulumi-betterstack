package provider

import (
	"encoding/json"
	"testing"
)

// A fixed-value input accepts the enum or any string, so the enums break no existing program.
func TestFixedValueInputsAreAStringOrAnEnum(t *testing.T) {
	var monitor struct {
		InputProperties map[string]struct {
			OneOf []struct {
				Type string `json:"type"`
				Ref  string `json:"$ref"`
			} `json:"oneOf"`
		} `json:"inputProperties"`
	}
	if err := json.Unmarshal(generatedSchema(t).Resources["betterstack:index/monitor:Monitor"], &monitor); err != nil {
		t.Fatal(err)
	}
	one := monitor.InputProperties["monitorType"].OneOf
	if len(one) != 2 || one[0].Type != "string" || one[1].Ref != "#/types/betterstack:index/monitorType:MonitorType" {
		t.Errorf("monitorType = %+v, want string or the MonitorType enum", one)
	}
}

func TestEnumMemberNames(t *testing.T) {
	for value, want := range map[string]string{"expected_status_code": "ExpectedStatusCode", "syslog-ng": "SyslogNg", "Monitor": "Monitor", "tcp": "Tcp"} {
		if got := enumMemberName(value); got != want {
			t.Errorf("enumMemberName(%q) = %q, want %q", value, got, want)
		}
	}
}
