package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

// maskKey is the server secret used by the keyed masking strategies (tokenize,
// fpe). It is installed once at start-up via SetMaskKey from configuration.
// When unset it falls back to an insecure development default and the server
// logs a warning. In production this secret must come from a secret manager /
// KMS and must never be committed to config, or tokenized/fpe values become
// reversible by anyone with the binary.
var maskKey = []byte("aegis-dev-mask-secret-change-me")

// SetMaskKey installs the server secret used for keyed masking strategies.
// An empty secret leaves the insecure development default in place.
func SetMaskKey(secret string) {
	if secret != "" {
		maskKey = []byte(secret)
	}
}

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
	case "tokenize":
		return tokenize(s)
	case "fpe":
		return fpe(s)
	default:
		return s
	}
}

// ---- keyed masking strategies ---------------------------------------------
//
// tokenize and fpe are deterministic, keyed transforms: the same input under
// the same maskKey always yields the same output. That lets downstream
// analytics/joins keep working on the masked value while the raw PII never
// leaves the platform. They are reversible only with the server key.

const fpeTokenLen = 24 // length of a tokenize() pseudonym

// tokenize returns a deterministic, opaque pseudonym for s. Because the output
// is stable per (input, key), tokenized identifiers can still be grouped or
// joined downstream without exposing the original value.
func tokenize(s string) string {
	if s == "" {
		return s
	}
	mac := hmac.New(sha256.New, maskKey)
	mac.Write([]byte(s))
	return base62(mac.Sum(nil))[:fpeTokenLen]
}

var b62Alphabet = []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")

// base62 encodes a byte slice into a base62 string (big-endian, no padding).
func base62(b []byte) string {
	x := new(big.Int).SetBytes(b)
	if x.Sign() == 0 {
		return "0"
	}
	base := big.NewInt(62)
	mod := new(big.Int)
	var out []byte
	for x.Sign() > 0 {
		x.DivMod(x, base, mod)
		out = append(out, b62Alphabet[mod.Int64()])
	}
	// out is little-endian; reverse to big-endian
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

const fpeRounds = 10 // Feistel rounds; 10 is ample for short PII fields

// fpe applies format-preserving encryption to a digit string, keeping the
// length and numeric form unchanged (e.g. a 16-digit card stays 16 digits).
// Non-digit input falls back to tokenize so it is still masked. The transform
// is reversible with the server key via FpeDecrypt, which lets authorized
// backend jobs re-identify values without shipping raw PII through the app.
func fpe(s string) string {
	if s == "" {
		return s
	}
	if !isAllDigits(s) {
		return tokenize(s)
	}
	return fpeEncryptDigits(s)
}

// FpeDecrypt reverses fpe for a digit string. It is exported for authorized
// re-identification paths (e.g. admin tooling) and for testing; it is NOT
// invoked by the masking executor, which only encrypts.
func FpeDecrypt(s string) string {
	if s == "" {
		return s
	}
	if !isAllDigits(s) {
		return s
	}
	return fpeDecryptDigits(s)
}

func fpeEncryptDigits(digits string) string {
	n := len(digits)
	if n == 1 {
		return string(fpeSingleDigit(digits[0], true))
	}
	leftW, rightW := n/2, n-n/2
	left, right := digits[:leftW], digits[leftW:]
	for r := 0; r < fpeRounds; r++ {
		f := fpeF(right, r, leftW)
		newRight := (atoi(left) + f) % pow10(leftW)
		left, right = right, fmt.Sprintf("%0*d", leftW, newRight)
		leftW, rightW = rightW, leftW
	}
	return left + right
}

func fpeDecryptDigits(digits string) string {
	n := len(digits)
	if n == 1 {
		return string(fpeSingleDigit(digits[0], false))
	}
	leftW, rightW := n/2, n-n/2
	left, right := digits[:leftW], digits[leftW:]
	for r := fpeRounds - 1; r >= 0; r-- {
		// left,right == (L_{r+1}, R_{r+1}); R_r = left, L_r = (R_{r+1} - f(R_r)) mod
		f := fpeF(left, r, leftW)
		leftNum := (atoi(right) - f) % pow10(leftW)
		if leftNum < 0 {
			leftNum += pow10(leftW)
		}
		newRight := left
		left = fmt.Sprintf("%0*d", leftW, leftNum)
		right = newRight
		leftW, rightW = rightW, leftW
	}
	return left + right
}

// fpeF is the Feistel round function: HMAC-SHA256(key, "fpe-v1" | round | block)
// reduced modulo 10^width. Domain separation ("fpe-v1") keeps it distinct from
// tokenize() and from any future keyed strategy.
func fpeF(block string, round, width int) int {
	if width <= 0 {
		return 0
	}
	mac := hmac.New(sha256.New, maskKey)
	mac.Write([]byte("fpe-v1"))
	mac.Write([]byte{byte(round)})
	mac.Write([]byte(block))
	sum := mac.Sum(nil)
	var v uint64
	for i := 0; i < 8; i++ {
		v = (v << 8) | uint64(sum[i])
	}
	return int(v % uint64(pow10(width)))
}

// fpeSingleDigit applies a keyed affine bijection over Z/10Z to a single
// digit. A single-digit value has no "other half" to drive a Feistel round, so
// we use c = (a*p + b) mod 10 with a coprime to 10 (hence invertible) and b a
// key-derived offset. The (a,b)=(1,0) identity case is skipped so the transform
// is never a no-op.
func fpeSingleDigit(d byte, encrypt bool) byte {
	mac := hmac.New(sha256.New, maskKey)
	mac.Write([]byte("fpe1"))
	mac.Write([]byte{0})
	sum := mac.Sum(nil)
	a := []int{1, 3, 7, 9}[int(sum[0])%4]
	b := int(sum[1]) % 10
	if a == 1 && b == 0 {
		b = 1
	}
	ainv := map[int]int{1: 1, 3: 7, 7: 3, 9: 9}[a]
	p := int(d - '0')
	if encrypt {
		return byte('0' + (p*a+b)%10)
	}
	return byte('0' + (ainv*((p-b+10)%10))%10)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func atoi(d string) int {
	n := 0
	for i := 0; i < len(d); i++ {
		n = n*10 + int(d[i]-'0')
	}
	return n
}

// pow10tab caches small powers of ten to avoid repeated overflow-prone math.
var pow10tab = func() []int {
	t := make([]int, 32)
	t[0] = 1
	for i := 1; i < len(t); i++ {
		t[i] = t[i-1] * 10
	}
	return t
}()

func pow10(k int) int {
	if k <= 0 {
		return 1
	}
	if k < len(pow10tab) {
		return pow10tab[k]
	}
	p := 1
	for i := 0; i < k; i++ {
		p *= 10
	}
	return p
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
