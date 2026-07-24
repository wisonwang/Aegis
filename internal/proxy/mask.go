package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

// colAction describes how one result column should be handled: kept as-is,
// dropped (denied / not allowed), or kept but value-masked.
type colAction struct {
	keep bool
	mask *store.MaskSpec
}

// columnActions resolves, per result column, whether to keep it and which mask
// (if any) to apply. Denied columns are always dropped; masking is applied
// only when the column is otherwise visible.
func columnActions(cols, denied, allowed []string, masks map[string]store.MaskSpec) ([]colAction, []string) {
	deniedSet := toSet(denied)
	allowSet := toSet(allowed)
	allowActive := len(allowed) > 0
	actions := make([]colAction, len(cols))
	outCols := make([]string, 0, len(cols))
	for i, c := range cols {
		lc := strings.ToLower(c)
		if deniedSet[lc] {
			actions[i] = colAction{keep: false}
			continue
		}
		if allowActive && !allowSet[lc] {
			actions[i] = colAction{keep: false}
			continue
		}
		a := colAction{keep: true}
		if m, ok := masks[lc]; ok {
			a.mask = &m
		}
		actions[i] = a
		outCols = append(outCols, c)
	}
	return actions, outCols
}

// applyMask transforms a single raw cell value according to a masking strategy.
// nil values pass through untouched; everything else is coerced to a string
// (so masked output is always a string, never the original typed value).
func applyMask(strategy string, keep int, raw interface{}) interface{} {
	if raw == nil {
		return nil
	}
	s := toString(raw)
	if s == "" {
		return s
	}
	switch strategy {
	case "phone":
		return maskPhone(s)
	case "email":
		return maskEmail(s)
	case "card":
		return maskCard(s)
	case "hash":
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])[:16]
	case "redact":
		return "***"
	case "partial":
		return maskKeepEdge(s, keepOrDefault(keep, 2), keepOrDefault(keep, 2))
	default:
		return s
	}
}

// ---- strategy implementations ----------------------------------------------

var digitRe = regexp.MustCompile(`\d`)

func toString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maskPhone keeps the first 3 and last 4 digits, masking the middle.
// e.g. 13812345678 -> 138****5678 (shorter numbers keep 1+1).
func maskPhone(s string) string {
	d := digitsOnly(s)
	if d == "" {
		return maskKeepEdge(s, 2, 2)
	}
	head, tail := 3, 4
	if len(d) <= 6 {
		head, tail = 1, 1
	}
	return maskKeepEdge(d, head, tail)
}

// maskEmail keeps the first character of the local part and the full domain.
// e.g. ops@acme.com -> o***@acme.com
func maskEmail(s string) string {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return maskKeepEdge(s, 2, 2)
	}
	local := s[:at]
	domain := s[at:] // includes the leading '@'
	if local == "" {
		return "***" + domain
	}
	return string(local[0]) + "***" + domain
}

// maskCard keeps only the last 4 digits.
// e.g. 4111111111111111 -> ************1111
func maskCard(s string) string {
	d := digitsOnly(s)
	if d == "" {
		return maskKeepEdge(s, 2, 2)
	}
	if len(d) <= 4 {
		return strings.Repeat("*", len(d))
	}
	return strings.Repeat("*", len(d)-4) + d[len(d)-4:]
}

// maskKeepEdge keeps `head` characters from the start and `tail` from the end,
// replacing the middle with '*'. If the string is too short to keep both
// edges, it keeps only the first and last character and masks the middle.
func maskKeepEdge(s string, head, tail int) string {
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	n := len(s)
	if n <= head+tail {
		if n <= 1 {
			return strings.Repeat("*", n)
		}
		return string(s[0]) + strings.Repeat("*", n-2) + string(s[n-1])
	}
	return s[:head] + strings.Repeat("*", n-head-tail) + s[n-tail:]
}

func keepOrDefault(keep, def int) int {
	if keep <= 0 {
		return def
	}
	return keep
}
