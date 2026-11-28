package provider

import (
	"context"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// recordingSink keeps what the provider logs, as the engine's host would show it.
type recordingSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *recordingSink) Log(_ context.Context, sev diag.Severity, _ resource.URN, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sev == diag.Error {
		s.lines = append(s.lines, msg)
	}
	return nil
}

func (s *recordingSink) LogStatus(context.Context, diag.Severity, resource.URN, string) error {
	return nil
}

func (s *recordingSink) errors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func TestReturnedErrorSinkDropsOnlyARepeatedError(t *testing.T) {
	rec := &recordingSink{}
	sink := returnedErrorSink{rec}
	ctx := context.Background()
	for _, msg := range []string{
		"[ERROR] Deletion protection is on. Set it off.: Deletion protection is on. Set it off.",
		"[ERROR] The API answered 422: Invalid request",
		"[ERROR] a: b",
	} {
		_ = sink.Log(ctx, diag.Error, "", msg)
	}
	_ = sink.Log(ctx, diag.Warning, "", "[WARNING] x: x")
	got := rec.errors()
	if len(got) != 2 || got[0] != "[ERROR] The API answered 422: Invalid request" || got[1] != "[ERROR] a: b" {
		t.Fatalf("only the error that repeats itself may be dropped, got %q", got)
	}
}
