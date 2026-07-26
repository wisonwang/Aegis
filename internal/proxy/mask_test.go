package proxy

import (
	"os"
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func TestMain(m *testing.M) {
	SetMaskKey("unit-test-secret")
	os.Exit(m.Run())
}

func TestApplyMask(t *testing.T) {
	cases := []struct {
		strategy string
		keep     int
		in       interface{}
		want     string
	}{
		{"phone", 0, "13812345678", "138****5678"},
		{"phone", 0, "12345", "1***5"},
		{"email", 0, "ops@acme.com", "o***@acme.com"},
		{"email", 0, "zhangwei@corp.cn", "z***@corp.cn"},
		{"card", 0, "4111111111111111", "************1111"},
		{"card", 0, "1234", "****"},
		{"hash", 0, "secret", "2bb80d537b1da3e3"}, // sha256("secret")[:16]
		{"redact", 0, "anything", "***"},
		{"partial", 2, "Acme Corp", "Ac*****rp"},
		{"partial", 1, "Acme Corp", "A*******p"},
		{"phone", 0, nil, ""},   // nil passes through
		{"bogus", 0, "x", "x"},  // unknown strategy = unchanged
	}
	for _, c := range cases {
		got := applyMask(c.strategy, c.keep, c.in)
		if c.in == nil {
			if got != nil {
				t.Errorf("applyMask(%q,%v) nil in => nil, got %v", c.strategy, c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("applyMask(%q,%d,%v) = %q, want %q", c.strategy, c.keep, c.in, got, c.want)
		}
	}
}

func TestColumnActions(t *testing.T) {
	cols := []string{"id", "name", "phone", "secret"}
	masks := map[string]store.MaskSpec{
		"phone":  {Column: "phone", Strategy: "phone"},
		"secret": {Column: "secret", Strategy: "redact"},
	}
	actions, outCols := columnActions(cols, []string{"secret"}, nil, masks)
	// "secret" is denied -> dropped; "phone" kept+masked; id/name kept.
	if len(outCols) != 3 || outCols[0] != "id" || outCols[1] != "name" || outCols[2] != "phone" {
		t.Fatalf("unexpected outCols: %v", outCols)
	}
	if actions[3].keep { // secret index 3
		t.Errorf("denied column should not be kept")
	}
	if !actions[2].keep || actions[2].mask == nil || actions[2].mask.Strategy != "phone" {
		t.Errorf("phone should be kept and masked, got %+v", actions[2])
	}
}

func TestTokenize(t *testing.T) {
	SetMaskKey("unit-test-secret")
	a := tokenize("alice@example.com")
	// deterministic per (input, key)
	if b := tokenize("alice@example.com"); a != b {
		t.Fatalf("tokenize not deterministic: %q vs %q", a, b)
	}
	if len(a) != fpeTokenLen {
		t.Fatalf("token length = %d, want %d", len(a), fpeTokenLen)
	}
	if a == "alice@example.com" {
		t.Fatal("tokenize must not echo the input")
	}
	if tokenize("bob@example.com") == a {
		t.Fatal("different inputs produced identical tokens")
	}
	// key rotation changes the token
	SetMaskKey("other-secret")
	if tokenize("alice@example.com") == a {
		t.Fatal("token should change when the key changes")
	}
	SetMaskKey("unit-test-secret")
}

func TestFPE(t *testing.T) {
	SetMaskKey("unit-test-secret")
	digits := "4111111111111111"
	out := fpe(digits)
	if len(out) != len(digits) {
		t.Fatalf("fpe changed length: %q (len %d)", out, len(out))
	}
	if !isAllDigits(out) {
		t.Fatalf("fpe output not all digits: %q", out)
	}
	if out == digits {
		t.Fatal("fpe must transform the input")
	}
	if fpe(digits) != out {
		t.Fatal("fpe not deterministic")
	}
	if fpe("4111111111111112") == out {
		t.Fatal("fpe collided for different inputs")
	}
	// non-digit input falls back to tokenize (opaque, fixed length)
	nd := fpe("abc-def")
	if len(nd) != fpeTokenLen {
		t.Fatalf("non-digit fpe fallback length = %d, want %d", len(nd), fpeTokenLen)
	}
	// round-trip decryption restores the original value
	if got := FpeDecrypt(out); got != digits {
		t.Fatalf("FpeDecrypt(%q) = %q, want %q", out, got, digits)
	}
	// single digit edge case
	if got := fpe("7"); got == "7" || !isAllDigits(got) || len(got) != 1 {
		t.Fatalf("fpe single digit = %q", got)
	}
	if FpeDecrypt(fpe("7")) != "7" {
		t.Fatal("single-digit fpe round-trip failed")
	}
}
