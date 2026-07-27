package proxy

import (
	"testing"

	"github.com/wisonwang/aegis/internal/store"
)

func TestRecommendMask(t *testing.T) {
	jsonTags := func(items ...string) string {
		if len(items) == 0 {
			return ""
		}
		out := "["
		for i, it := range items {
			if i > 0 {
				out += ","
			}
			out += `"` + it + `"`
		}
		return out + "]"
	}

	cases := []struct {
		name        string
		dc          store.DataClassification
		wantStrat   string
		wantKeep    int
		wantApplied bool
	}{
		// 1) Precise tags win.
		{"pii:phone tag", store.DataClassification{ColumnName: "phone", Level: "pii", Tags: jsonTags("pii:phone")}, "phone", 0, true},
		{"pii:mobile tag", store.DataClassification{ColumnName: "mobile", Level: "pii", Tags: jsonTags("pii:mobile")}, "phone", 0, true},
		{"pii:email tag", store.DataClassification{ColumnName: "email", Level: "pii", Tags: jsonTags("pii:email")}, "email", 0, true},
		{"pii:card tag", store.DataClassification{ColumnName: "card_no", Level: "pii", Tags: jsonTags("pii:card")}, "card", 0, true},
		{"pii:bank tag", store.DataClassification{ColumnName: "bank_account", Level: "pii", Tags: jsonTags("pii:bank")}, "card", 0, true},
		{"pii:idcard tag", store.DataClassification{ColumnName: "id_card", Level: "pii", Tags: jsonTags("pii:idcard")}, "fpe", 0, true},
		{"pii:ssn tag", store.DataClassification{ColumnName: "ssn", Level: "pii", Tags: jsonTags("pii:ssn")}, "fpe", 0, true},
		{"pii:name tag", store.DataClassification{ColumnName: "name", Level: "pii", Tags: jsonTags("pii:name")}, "partial", 1, true},

		// 2) Level-gated column-name heuristics.
		{"restricted + mobile col", store.DataClassification{ColumnName: "mobile_phone", Level: "restricted"}, "phone", 0, true},
		{"pii + email col", store.DataClassification{ColumnName: "contact_email", Level: "pii"}, "email", 0, true},
		{"pii + bank col", store.DataClassification{ColumnName: "bank_card", Level: "pii"}, "card", 0, true},
		{"pii + idcard col", store.DataClassification{ColumnName: "id_card_no", Level: "pii"}, "fpe", 0, true},
		{"pii + name col", store.DataClassification{ColumnName: "customer_name", Level: "pii"}, "partial", 1, true},
		{"pii generic col", store.DataClassification{ColumnName: "notes", Level: "pii"}, "tokenize", 0, true},
		{"restricted generic col", store.DataClassification{ColumnName: "remark", Level: "restricted"}, "tokenize", 0, true},

		// 3) Confidential / financial figures.
		{"confidential amount", store.DataClassification{ColumnName: "amount", Level: "confidential"}, "partial", 2, true},
		{"confidential + financial tag", store.DataClassification{ColumnName: "balance", Level: "confidential", Tags: jsonTags("financial", "money")}, "partial", 2, true},
		{"internal level + financial tag", store.DataClassification{ColumnName: "salary", Level: "internal", Tags: jsonTags("financial")}, "partial", 2, true},

		// 4) No masking warranted.
		{"internal level", store.DataClassification{ColumnName: "remark", Level: "internal"}, "", 0, false},
		{"public level", store.DataClassification{ColumnName: "status", Level: "public"}, "", 0, false},
		{"empty level", store.DataClassification{ColumnName: "x"}, "", 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			strat, keep, _, applied := RecommendMask(c.dc)
			if applied != c.wantApplied {
				t.Errorf("applicable = %v, want %v", applied, c.wantApplied)
			}
			if strat != c.wantStrat {
				t.Errorf("strategy = %q, want %q", strat, c.wantStrat)
			}
			if keep != c.wantKeep {
				t.Errorf("keep = %d, want %d", keep, c.wantKeep)
			}
			if applied && !validMaskStrategyLocal(strat) {
				t.Errorf("recommended strategy %q is not a valid mask strategy", strat)
			}
		})
	}
}

// validMaskStrategyLocal mirrors api.validMaskStrategy so the test can assert
// the recommender never proposes an unsupported strategy without importing the
// api package.
func validMaskStrategyLocal(s string) bool {
	switch s {
	case "phone", "email", "card", "hash", "redact", "partial", "tokenize", "fpe":
		return true
	}
	return false
}

// TestRecommendMask_TableLevelSkipped is a guard documenting that table-level
// (ColumnName=="") classifications are not mapped to column masks by callers;
// the function itself returns not-applicable for an empty column with no tags.
func TestRecommendMask_TableLevel(t *testing.T) {
	_, _, _, applied := RecommendMask(store.DataClassification{TableName: "customers", ColumnName: "", Level: "restricted"})
	if applied {
		t.Errorf("table-level classification should not produce a column mask")
	}
}
