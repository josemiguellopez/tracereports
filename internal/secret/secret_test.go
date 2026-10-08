package secret

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// claves ficticias de prueba (32 bytes)
var (
	keyA = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	keyB = base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
)

func mustBox(t *testing.T, cur, prev string) *Box {
	t.Helper()
	b, err := New(cur, prev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := mustBox(t, keyA, "")
	sealed, err := b.Seal("ai.api_key", "sk-live-SECRET-1234")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "sk-live") || strings.Contains(sealed, "SECRET") || !IsSealed(sealed) {
		t.Fatalf("stored value must not contain the secret: %s", sealed)
	}
	again, _ := b.Seal("ai.api_key", "sk-live-SECRET-1234")
	if again == sealed {
		t.Fatal("each seal uses a fresh nonce")
	}
	got, err := b.Open("ai.api_key", sealed)
	if err != nil || got != "sk-live-SECRET-1234" {
		t.Fatalf("open: %q %v", got, err)
	}
	// otro proceso con la misma clave (reinicio) lo lee igual
	if got, err := mustBox(t, keyA, "").Open("ai.api_key", sealed); err != nil || got != "sk-live-SECRET-1234" {
		t.Fatalf("after restart: %q %v", got, err)
	}
	// atado al nombre del ajuste
	if _, err := b.Open("other.setting", sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("moved to another setting: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	sealed, _ := mustBox(t, keyA, "").Seal("ai.api_key", "sk-x")
	if _, err := (&Box{}).Open("ai.api_key", sealed); !errors.Is(err, ErrNoKey) {
		t.Fatalf("missing key: %v", err)
	}
	var nilBox *Box
	if _, err := nilBox.Open("ai.api_key", sealed); !errors.Is(err, ErrNoKey) {
		t.Fatalf("nil box: %v", err)
	}
	if _, err := mustBox(t, keyB, "").Open("ai.api_key", sealed); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	tampered := sealed[:len(sealed)-3] + "AAA"
	if _, err := mustBox(t, keyA, "").Open("ai.api_key", tampered); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := (&Box{}).Seal("ai.api_key", "x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("seal without key: %v", err)
	}
	// valores antiguos en claro: se devuelven igual (transición) y piden recifrado
	if v, err := nilBox.Open("ai.api_key", "legacy-plain"); err != nil || v != "legacy-plain" {
		t.Fatalf("legacy: %q %v", v, err)
	}
	if !mustBox(t, keyA, "").NeedsReseal("legacy-plain") || mustBox(t, keyA, "").NeedsReseal(sealed) {
		t.Fatal("NeedsReseal")
	}
}

func TestRotationWithPreviousKey(t *testing.T) {
	old, _ := mustBox(t, keyA, "").Seal("ai.api_key", "sk-rotate")
	b := mustBox(t, keyB, keyA)
	if v, err := b.Open("ai.api_key", old); err != nil || v != "sk-rotate" {
		t.Fatalf("previous key must still read: %q %v", v, err)
	}
	if !b.NeedsReseal(old) {
		t.Fatal("a value with the previous key needs reseal")
	}
	if _, err := New("", keyA); err == nil {
		t.Fatal("previous without current must fail")
	}
}

func TestParseKey(t *testing.T) {
	for _, k := range []string{keyA, strings.TrimRight(keyA, "="), strings.Repeat("ab", 32)} {
		if _, err := ParseKey(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString([]byte("16-bytes-only!!!"))} {
		if _, err := ParseKey(k); err == nil {
			t.Errorf("%q must be rejected", k)
		}
	}
	if _, err := New("not-a-key", ""); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_SECRET_KEY") {
		t.Fatalf("bad env key must name the variable: %v", err)
	}
}
