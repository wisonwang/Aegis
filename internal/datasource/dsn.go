package datasource

import (
	"fmt"
	"net/url"
	"regexp"
)

// DSNDocsURL is the canonical human-readable reference for DSN formats. It is
// surfaced in API responses and validation errors so operators can self-serve
// instead of guessing the connection-string shape. Operators may point this at
// an internal docs mirror if they fork Aegis.
const DSNDocsURL = "https://github.com/wisonwang/Aegis/blob/main/docs/datasource-dsn.md"

// MaskedPassword is the placeholder substituted for a real credential in any
// DSN echoed back by the API. The presence of this exact string in a submitted
// DSN is how the update path detects "operator pasted the masked value back"
// and refuses to overwrite the real secret.
const MaskedPassword = "****"

// mysqlDriverDSN matches the go-sql-driver/mysql DSN form:
//
//	user:password@tcp(host:port)/dbname[?params]
//
// The user segment cannot contain ':', and the password is everything up to
// the literal "@tcp(". It is intentionally anchored so it never fires on
// URL-style DSNs (scheme://...), which are handled by url.Parse below.
var mysqlDriverDSN = regexp.MustCompile(`^([^:]+):(.*)@tcp\(`)

// MaskDSN returns dsn with the credential redacted. Supported shapes:
//   - URL-style DSNs (postgres://, mysql://, mongodb://, http(s)://,
//     clickhouse, trino, presto, sqlserver, ...): the password in the userinfo
//     is replaced with MaskedPassword.
//   - the MySQL driver DSN (user:pass@tcp(host:port)/db): same treatment.
//   - sqlite file paths and any other shape with no extractable secret:
//     returned unchanged.
//
// Host, port, database name and query parameters stay visible so operators can
// still tell connections apart; only the password is hidden. This is the value
// the admin list and MCP list_datasources endpoints return — the raw DSN never
// leaves the server on a list path.
func MaskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// URL-style: scheme://user:pass@host/...
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			// Rebuild with a literal masked password. Using url.UserPassword +
			// u.String() would percent-encode '*' (%2A), which is inconsistent
			// with the MySQL-driver branch below and noisier to read.
			out := u.Scheme + "://" + u.User.Username() + ":" + MaskedPassword + "@" + u.Host
			if u.Path != "" {
				out += u.Path
			}
			if u.RawQuery != "" {
				out += "?" + u.RawQuery
			}
			if u.Fragment != "" {
				out += "#" + u.Fragment
			}
			return out
		}
		return dsn // user present but no password (nothing to hide)
	}
	// MySQL driver form (no scheme): user:pass@tcp(...)
	if mysqlDriverDSN.MatchString(dsn) {
		return mysqlDriverDSN.ReplaceAllString(dsn, "${1}:"+MaskedPassword+"@tcp(")
	}
	return dsn
}

// IsMasked reports whether dsn already contains the redaction placeholder.
// Used by the update path to refuse masked values that an operator pasted back
// from a list response.
func IsMasked(dsn string) bool {
	return len(dsn) > 0 && contains(dsn, MaskedPassword)
}

// ValidateDSN performs a lenient structural check of dsn for the given
// datasource type. It never rejects a DSN Aegis can plausibly open — the goal
// is to catch typos early and point operators to DSNDocsURL, not to be a full
// connection tester (that still happens lazily at query time). An empty dsn is
// only accepted for sqlite, which may resolve a file path or :memory:.
func ValidateDSN(dsType, dsn string) error {
	t := NormalizeType(dsType)
	if dsn == "" {
		if t == "sqlite" {
			return nil
		}
		return fmt.Errorf("DSN is required for type %q; see %s", t, DSNDocsURL)
	}
	switch t {
	case "sqlite":
		return nil // file path or :memory:
	case "mysql":
		if mysqlDriverDSN.MatchString(dsn) {
			return nil
		}
		if u, err := url.Parse(dsn); err == nil && u.Host != "" {
			return nil
		}
		return fmt.Errorf("invalid MySQL DSN %q; expected 'user:pass@tcp(host:port)/db' or a mysql:// URL; see %s", dsn, DSNDocsURL)
	default:
		u, err := url.Parse(dsn)
		if err != nil || u.Host == "" {
			return fmt.Errorf("invalid DSN %q for type %q; expected a URL like 'scheme://user:pass@host[:port]/db'; see %s", dsn, t, DSNDocsURL)
		}
		return nil
	}
}

// contains is a tiny helper so dsn.go does not depend on strings (kept local to
// avoid import churn in this low-level package).
func contains(s, sub string) bool {
	n := len(sub)
	if n == 0 || n > len(s) {
		return false
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return true
		}
	}
	return false
}
