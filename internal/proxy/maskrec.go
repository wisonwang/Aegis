package proxy

import (
	"encoding/json"
	"strings"

	"github.com/wisonwang/aegis/internal/store"
)

// RecommendMask derives a sensible default dynamic-masking strategy from a
// column's data classification (level + tags) and its physical column name.
// It implements the blueprint's short-term item "数据分类分级：按敏感级别自动
// 推荐默认脱敏策略": operators classify a column once, then let Aegis propose
// (and optionally apply) a masking rule instead of hand-tuning every PII column.
//
// Precedence: precise tags (pii:phone, pii:email, ...) win over coarse level
// heuristics, which in turn win over column-name guesses. Returns
// applicable=false when no masking is warranted (public / internal columns),
// so callers can skip them outright. Keep is only meaningful for "partial".
func RecommendMask(dc store.DataClassification) (strategy string, keep int, reason string, applicable bool) {
	tags := parseClassTags(dc.Tags)
	col := strings.ToLower(dc.ColumnName)
	level := strings.ToLower(dc.Level)

	// Masks are per-column; a table-level classification (empty column name) is
	// not a column-mask candidate. Callers also skip these, but the guard keeps
	// the contract self-consistent.
	if dc.ColumnName == "" {
		return "", 0, "表级分类非列级，无需列脱敏", false
	}

	// 1) Precise tag-driven mapping — explicit intent always wins.
	for _, t := range tags {
		switch strings.ToLower(t) {
		case "pii:phone", "pii:mobile":
			return "phone", 0, "标签 pii:phone → 手机号掩码（保留前3后4）", true
		case "pii:email":
			return "email", 0, "标签 pii:email → 邮箱掩码（保留域名）", true
		case "pii:card", "pii:bank", "pii:account":
			return "card", 0, "标签 pii:card → 卡号掩码（保留后4）", true
		case "pii:idcard", "pii:ssn":
			return "fpe", 0, "标签 pii:idcard → 格式保留加密（保长保型，密钥可还原）", true
		case "pii:name":
			return "partial", 1, "标签 pii:name → 部分掩码（仅保留首尾）", true
		}
	}

	// 2) Level-gated column-name heuristics when tags are generic.
		if level == "pii" || level == "restricted" {
			switch {
			case matchAny(col, "phone", "mobile", "tel"):
				return "phone", 0, "列名含 phone/mobile/tel 且级别 pii/restricted → 手机号掩码", true
			case matchAny(col, "email", "mail"):
				return "email", 0, "列名含 email/mail 且级别 pii/restricted → 邮箱掩码", true
			case matchAny(col, "idcard", "id_card", "idno", "ssn", "identity"):
				return "fpe", 0, "列名含证件号特征 且级别 pii/restricted → 格式保留加密", true
			case matchAny(col, "card", "bank", "account"):
				return "card", 0, "列名含 card/bank/account 且级别 pii/restricted → 卡号掩码", true
			case matchAny(col, "name", "realname", "fullname"):
				return "partial", 1, "列名含 name 且级别 pii/restricted → 部分掩码", true
			default:
				// Generic personal / restricted data: deterministic tokenization
				// keeps downstream joinability without exposing the raw value.
				return "tokenize", 0, "级别 pii/restricted 且无更精确标签 → 确定性假名（可关联不可还原）", true
			}
		}

	// 3) Confidential / financial figures: show magnitude, hide exact value.
	if level == "confidential" || hasClassTag(tags, "financial", "money") {
		return "partial", 2, "级别 confidential / 财务字段 → 部分掩码（保留首尾各2位）", true
	}

	// public / internal: no masking warranted.
	return "", 0, "级别 " + level + " 无需脱敏", false
}

// parseClassTags safely decodes the JSON tag array stored on a classification.
func parseClassTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// matchAny reports whether s contains any of the substrings.
func matchAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// hasClassTag reports whether tags contains an exact match or a "prefix:" match
// for any of want (e.g. "financial" matches "financial" and "financial:xxx").
func hasClassTag(tags []string, want ...string) bool {
	for _, t := range tags {
		tl := strings.ToLower(t)
		for _, w := range want {
			if tl == w || strings.HasPrefix(tl, w+":") {
				return true
			}
		}
	}
	return false
}
