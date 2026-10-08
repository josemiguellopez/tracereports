package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConnectionUsageCountsInternalAttempts(t *testing.T) {
	for _, provider := range []string{"openai_compatible", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			first, last := reply{status: 400, body: `{"error":{"message":"json_schema unsupported"}}`}, reply{status: 200, body: openAIOK}
			if provider == "anthropic" {
				first = reply{status: 503, body: overloaded, header: map[string]string{"Retry-After-Ms": "10"}}
				last = reply{status: 200, body: anthropicOK}
			}
			server, hits := scriptedProvider(t, first, last)
			analyzer := New(usageStore(t))
			cfg := Config{Provider: provider, BaseURL: server.URL, Model: "fake-test-model", APIKey: "fake-test-secret-123456"}
			if _, err := analyzer.Test(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			usage := usageNow(t, analyzer.store)
			total := usage.Today
			if hits.Load() != 2 || total.Calls != 2 || total.Errors != 1 || total.Untracked != 1 || total.InputTokens != 10 || total.OutputTokens != 2 {
				t.Fatalf("provider=%s HTTP=%d usage=%+v", provider, hits.Load(), total)
			}
			if len(usage.ByKind) != 1 || usage.ByKind[0].Kind != UsageTest {
				t.Fatalf("connection test kind: %+v", usage.ByKind)
			}
		})
	}
}

func TestConnectionUsageUsesSuppliedConfig(t *testing.T) {
	active, activeHits := scriptedProvider(t, reply{status: 200, body: openAIOK})
	supplied, hits := scriptedProvider(t, reply{status: 400, body: `{"error":{"message":"unsupported schema"}}`}, reply{status: 200, body: openAIOK})
	analyzer := New(usageStore(t))
	if err := analyzer.SetConfig(Config{Provider: "openai_compatible", BaseURL: active.URL, Model: "fake-active-model"}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "openai_compatible", BaseURL: supplied.URL, Model: "fake-supplied-model"}
	if _, err := analyzer.Test(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	usage := usageNow(t, analyzer.store)
	if activeHits.Load() != 0 || hits.Load() != 2 || len(usage.ByModel) != 1 || usage.ByModel[0].Model != cfg.Model || usage.ByModel[0].Calls != 2 {
		t.Fatalf("active=%d supplied=%d models=%+v", activeHits.Load(), hits.Load(), usage.ByModel)
	}
	if after := analyzer.Config(); after.BaseURL != active.URL || after.Model != "fake-active-model" {
		t.Fatalf("Test changed the active config: %+v", after)
	}
}

func TestConnectionUsageRecordsTerminalErrors(t *testing.T) {
	for _, provider := range []string{"openai_compatible", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			key := "fake-test-secret-123456"
			failure := reply{status: 400, body: `{"error":{"message":"bad request fake-test-secret-123456"}}`}
			expected := int64(2)
			if provider == "anthropic" {
				failure = reply{status: 503, body: `{"type":"error","error":{"type":"overloaded_error","message":"fake-test-secret-123456"}}`, header: map[string]string{"Retry-After-Ms": "10"}}
				expected = 3
			}
			server, hits := scriptedProvider(t, failure)
			analyzer := New(usageStore(t))
			_, err := analyzer.Test(t.Context(), Config{Provider: provider, BaseURL: server.URL, Model: "fake", APIKey: key})
			if err == nil || strings.Contains(err.Error(), key) {
				t.Fatalf("terminal error must be scrubbed: %v", err)
			}
			total := usageNow(t, analyzer.store).Today
			if int64(hits.Load()) != expected || total.Calls != expected || total.Errors != expected || total.Untracked != expected {
				t.Fatalf("HTTP=%d usage=%+v", hits.Load(), total)
			}
		})
	}
}

func TestConnectionUsageDoesNotCountCancellationAsAnotherCall(t *testing.T) {
	server, hits := scriptedProvider(t, reply{status: 503, body: overloaded, header: map[string]string{"Retry-After": "30"}})
	analyzer := New(usageStore(t))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for hits.Load() == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := analyzer.Test(ctx, Config{Provider: "anthropic", BaseURL: server.URL, Model: "fake", APIKey: "fake-test-secret-123456"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	total := usageNow(t, analyzer.store).Today
	if hits.Load() != 1 || total.Calls != 1 || total.Errors != 1 || total.Untracked != 1 || total.ErrorsBy["server"] != 1 {
		t.Fatalf("HTTP=%d usage=%+v", hits.Load(), total)
	}
}

func TestConnectionUsagePreservesRequestDeadline(t *testing.T) {
	server, hits := scriptedProvider(t, reply{status: 0})
	analyzer := New(usageStore(t))
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err := analyzer.Test(ctx, Config{Provider: "openai_compatible", BaseURL: server.URL, Model: "fake"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	total := usageNow(t, analyzer.store).Today
	if hits.Load() != 1 || total.Calls != 1 || total.Errors != 1 || total.ErrorsBy["timeout"] != 1 {
		t.Fatalf("HTTP=%d usage=%+v", hits.Load(), total)
	}
}
