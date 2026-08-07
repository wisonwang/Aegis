package datasource

import "testing"

func TestMaskDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "mysql driver form",
			in:   "root:s3cr3t@tcp(127.0.0.1:3306)/aegis?parseTime=true",
			want: "root:" + MaskedPassword + "@tcp(127.0.0.1:3306)/aegis?parseTime=true",
		},
		{
			name: "mysql url form",
			in:   "mysql://root:s3cr3t@db.example.com:3306/aegis",
			want: "mysql://root:" + MaskedPassword + "@db.example.com:3306/aegis",
		},
		{
			name: "postgres url form",
			in:   "postgres://app:pw@pg:5432/orders?sslmode=disable",
			want: "postgres://app:" + MaskedPassword + "@pg:5432/orders?sslmode=disable",
		},
		{
			name: "mongodb url form",
			in:   "mongodb://u:p@mongo:27017/test?authSource=admin",
			want: "mongodb://u:" + MaskedPassword + "@mongo:27017/test?authSource=admin",
		},
		{
			name: "elasticsearch url form",
			in:   "http://elastic:changeme@es:9200",
			want: "http://elastic:" + MaskedPassword + "@es:9200",
		},
		{
			name: "trino url form",
			in:   "https://user:pass@coordinator:8443?catalog=hive",
			want: "https://user:" + MaskedPassword + "@coordinator:8443?catalog=hive",
		},
		{
			name: "sqlite file path keeps unchanged",
			in:   "/var/lib/aegis/demo.db",
			want: "/var/lib/aegis/demo.db",
		},
		{
			name: "no-password mysql driver form keeps unchanged",
			in:   "root@tcp(127.0.0.1:3306)/aegis",
			want: "root@tcp(127.0.0.1:3306)/aegis",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskDSN(c.in); got != c.want {
				t.Fatalf("MaskDSN(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsMasked(t *testing.T) {
	if !IsMasked("root:" + MaskedPassword + "@tcp(127.0.0.1:3306)/db") {
		t.Fatal("expected masked DSN to be detected")
	}
	if IsMasked("root@tcp(127.0.0.1:3306)/db") {
		t.Fatal("plain DSN must not be reported as masked")
	}
	if IsMasked("") {
		t.Fatal("empty DSN must not be masked")
	}
}

func TestValidateDSN(t *testing.T) {
	ok := []struct{ typ, dsn string }{
		{"mysql", "root:pass@tcp(127.0.0.1:3306)/db"},
		{"mysql", "mysql://root:pass@db:3306/db"},
		{"postgres", "postgres://u:p@host:5432/db"},
		{"sqlite", "/tmp/x.db"},
		{"sqlite", ":memory:"},
		{"mongo", "mongodb://u:p@host:27017/db"},
		{"es", "http://u:p@host:9200"},
		{"trino", "https://u:p@host:8443?catalog=hive"},
	}
	for _, c := range ok {
		if err := ValidateDSN(c.typ, c.dsn); err != nil {
			t.Fatalf("ValidateDSN(%q,%q) unexpected error: %v", c.typ, c.dsn, err)
		}
	}

	bad := []struct{ typ, dsn string }{
		{"mysql", "not-a-dsn"},
		{"postgres", "justsomestring"},
		{"mongo", "mongodb:///db"}, // no host -> rejected
	}
	for _, c := range bad {
		if err := ValidateDSN(c.typ, c.dsn); err == nil {
			t.Fatalf("ValidateDSN(%q,%q) expected error, got nil", c.typ, c.dsn)
		}
	}

	// empty DSN rejected for non-sqlite, accepted for sqlite
	if err := ValidateDSN("mysql", ""); err == nil {
		t.Fatal("empty mysql DSN must be rejected")
	}
	if err := ValidateDSN("sqlite", ""); err != nil {
		t.Fatalf("empty sqlite DSN must be allowed: %v", err)
	}
}
