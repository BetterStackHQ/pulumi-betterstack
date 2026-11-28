package provider

import (
	"context"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// returnedErrorSink drops the log line the bridge writes for an error diagnostic whose summary and
// detail are the same text. The bridge logs every diagnostic as "[ERROR] <detail>: <summary>" and
// then fails the operation with the first error, written as the summary alone when it equals the
// detail. The provider gives its own refusals that shape (addProtectedError), so the failure already
// says it and the log would only repeat it. Other errors still reach the log.
type returnedErrorSink struct {
	LogSink
}

func (s returnedErrorSink) Log(ctx context.Context, sev diag.Severity, urn resource.URN, msg string) error {
	if sev == diag.Error && repeatsItself(strings.TrimPrefix(msg, "[ERROR] ")) {
		return nil
	}
	return s.LogSink.Log(ctx, sev, urn, msg)
}

// repeatsItself reports whether msg is "<text>: <text>", one text twice around the bridge's ": ".
func repeatsItself(msg string) bool {
	if len(msg) < 3 || (len(msg)-2)%2 != 0 {
		return false
	}
	half := (len(msg) - 2) / 2
	return msg[half:half+2] == ": " && msg[:half] == msg[half+2:]
}
