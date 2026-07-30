package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	_ "modernc.org/sqlite"
	"context"
)

// seedIfEmpty populates a self-contained demo tenant the first time the
// platform boots with an empty control plane. It demonstrates the three core
// capabilities: centralized auth, table/row/column governance, and a ready
// datasource for the DataAPI and MCP endpoints.
func seedIfEmpty(st *store.Store, cfg *config.Config) error {
	users, err := st.ListUsers("")
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return nil
	}

	// Roles (admin/analyst are system roles — protected from deletion and
	// from being granted/revoked via the API to prevent privilege escalation).
	_ = st.CreateRole(&store.Role{Name: "admin", Description: "平台管理员（超级用户，绕过行级治理）", System: true})
	_ = st.CreateRole(&store.Role{Name: "analyst", Description: "数据分析师（受表/行/列权限约束）", System: true})
	// Re-assert the system flag even when the role row pre-existed from an
	// older schema without the column.
	_ = st.SetRoleSystem("admin", true)
	_ = st.SetRoleSystem("analyst", true)

	// Users
	adminHash, _ := auth.HashPassword("admin123")
	_ = st.CreateUser(&store.User{Username: "admin", DisplayName: "Administrator", PasswordHash: adminHash})
	analystHash, _ := auth.HashPassword("analyst123")
	_ = st.CreateUser(&store.User{Username: "analyst", DisplayName: "Analyst", PasswordHash: analystHash, Attributes: `{"tenant":"acme"}`})
	// mcp-agent is a service account: it authenticates via API key only, so
	// it carries no password (password login is rejected for type=service).
	_ = st.CreateUser(&store.User{Username: "mcp-agent", DisplayName: "MCP Agent", Type: "service", Attributes: `{"tenant":"acme"}`})

	admin, _ := st.GetRole("admin")
	analyst, _ := st.GetRole("analyst")
	if a, _ := st.GetUserByUsername("admin"); a != nil && admin != nil {
		_ = st.AddUserRole(a.ID, admin.ID)
	}
	if u, _ := st.GetUserByUsername("analyst"); u != nil && analyst != nil {
		_ = st.AddUserRole(u.ID, analyst.ID)
	}
	if u, _ := st.GetUserByUsername("mcp-agent"); u != nil && analyst != nil {
		_ = st.AddUserRole(u.ID, analyst.ID)
	}

	// Demo datasource (SQLite file)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	demoPath := filepath.Join(cfg.DataDir, "demo.db")
	if err := buildDemoDB(demoPath); err != nil {
		return err
	}
	_ = st.CreateDataSource(context.Background(), &store.DataSource{Name: "demo", Type: "sqlite", DSN: demoPath})

	dsID, err := datasourceIDByName(st, "demo")
	if err != nil || dsID == "" {
		return fmt.Errorf("demo datasource not found after creation")
	}

	// Governance: analyst may SELECT orders/customers; orders is row-scoped to :tenant.
	_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "orders", Ops: "SELECT",
	})
	_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "customers", Ops: "SELECT",
	})
	_ = st.CreateRowPolicy(context.Background(), &store.RowPolicy{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "orders",
		Predicate: "tenant_id = :tenant", Priority: 10,
	})

	// Dynamic masking: the analyst keeps PII columns (phone/email) visible but
	// their values are masked, so AI supply stays useful without leaking PII.
	_ = st.UpsertColumnMask(context.Background(), &store.ColumnMask{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "customers",
		ColumnName: "phone", Strategy: "phone",
	})
	_ = st.UpsertColumnMask(context.Background(), &store.ColumnMask{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "customers",
		ColumnName: "email", Strategy: "email",
	})

	// Semantic layer: business descriptions so AI agents generate correct SQL.
	seedSemantics(st, dsID)

	// Data classification: mark PII / financial columns so AI agents know which
	// columns demand care (a metadata layer, independent of per-role masks).
	seedClassifications(st, dsID)

	// Auto-apply the recommended masking rules for the analyst role so a fresh
	// install already demonstrates column-level governance end-to-end. admin
	// bypasses masks, so we target the least-privilege analyst role. Any rule
	// already set manually above (phone/email) is a no-op upsert.
	seedRecommendedMasks(st, dsID, analyst.ID)

	// Dataset management demo: a curated, governed "paid orders" data product
	// over the demo source. The analyst may consume it; a row policy scopes it
	// to the caller's tenant, mirroring how a platform team would publish a
	// safe extract without exposing the raw table.
	_ = st.CreateDataset(context.Background(), &store.Dataset{
		Name:         "paid_orders",
		DisplayName:  "已支付订单",
		Description:  "已支付订单的只读数据产品，按租户隔离，供分析师消费。",
		DataSourceID: dsID,
		Definition:   "SELECT id, tenant_id, customer, amount, status FROM orders WHERE status = 'paid'",
		Status:       store.DatasetPublished,
		Fields:       `[{"name":"id","type":"integer"},{"name":"tenant_id","type":"text"},{"name":"customer","type":"text"},{"name":"amount","type":"real"},{"name":"status","type":"text"}]`,
	})
	pd, _ := st.GetDatasetByName(context.Background(), "paid_orders")
	if pd != nil {
		_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
			RoleID: analyst.ID, DataSourceID: dsID, TableName: "paid_orders", Ops: "SELECT",
		})
		_ = st.CreateRowPolicy(context.Background(), &store.RowPolicy{
			RoleID: analyst.ID, DataSourceID: dsID, TableName: "paid_orders",
			Predicate: "tenant_id = :tenant", Priority: 10,
		})
		// Dataset-level semantics enrich the catalog the agent sees.
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: "paid_orders", ColumnName: "amount",
			Description: "订单金额（仅含已支付订单），单位人民币元。",
			Synonyms:    `["金额","成交额"]`,
			Examples:    `["120.50","340.00"]`,
		})
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: "paid_orders", ColumnName: "customer",
			Description: "下单客户名称。", Synonyms: `["客户名"]`,
		})
	}
	return nil
}

// seedClassifications attaches sensitivity labels (PII / financial) to the
// demo tables and columns. This flows into the semantic catalog so AI agents
// are told which columns carry personal or financial data and must be handled
// with care. Like semantics it is a metadata layer, independent of per-role
// masks: a column can be classified AND masked at once.
func seedClassifications(st *store.Store, dsID string) {
	arr := func(items []string) string {
		if len(items) == 0 {
			return ""
		}
		b, _ := json.Marshal(items)
		return string(b)
	}
	up := func(table, col, level string, tags []string) {
		_ = st.UpsertClassification(context.Background(), &store.DataClassification{
			DataSourceID: dsID, TableName: table, ColumnName: col,
			Level: level, Tags: arr(tags),
		})
	}
	// Table-level marks: the customers table as a whole holds PII, orders holds
	// financial data. Column-level labels below refine these.
	up("customers", "", "restricted", []string{"pii"})
	up("orders", "", "confidential", []string{"financial"})
	// customers columns carry direct personal identifiers.
	up("customers", "name", "pii", []string{"pii:name", "contact"})
	up("customers", "phone", "pii", []string{"pii:phone", "contact"})
	up("customers", "email", "pii", []string{"pii:email", "contact"})
	// orders.amount is the financial figure.
	up("orders", "amount", "confidential", []string{"financial", "money"})
	// The published dataset inherits the same sensitivity for its amount column.
	up("paid_orders", "amount", "confidential", []string{"financial", "money"})
}

// seedRecommendedMasks turns the demo classifications into concrete masking
// rules for the given role, demonstrating the "classify once, mask automatically"
// workflow. It is idempotent against rules set manually elsewhere. Only runs on
// a fresh seed, so existing control planes are never mutated.
func seedRecommendedMasks(st *store.Store, dsID, roleID string) {
	if roleID == "" {
		return
	}
	cls, err := st.ListClassifications(context.Background(), dsID, "")
	if err != nil {
		return
	}
	for _, c := range cls {
		if c.ColumnName == "" {
			continue
		}
		strategy, keep, _, ok := proxy.RecommendMask(*c)
		if !ok {
			continue
		}
		_ = st.UpsertColumnMask(context.Background(), &store.ColumnMask{
			RoleID: roleID, DataSourceID: dsID, TableName: c.TableName,
			ColumnName: c.ColumnName, Strategy: strategy, Keep: keep,
		})
	}
}

// seedSemantics attaches business descriptions, synonyms and examples to the
// demo tables/columns. This is the "AI data-supply" layer: it turns a raw
// physical schema into something an LLM can map natural language onto.
func seedSemantics(st *store.Store, dsID string) {
	arr := func(items []string) string {
		if len(items) == 0 {
			return ""
		}
		b, _ := json.Marshal(items)
		return string(b)
	}
	up := func(table, col, desc string, synonyms, examples []string) {
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: table, ColumnName: col,
			Description: desc, Synonyms: arr(synonyms), Examples: arr(examples),
		})
	}
	// Tables (column_name = "").
	up("orders", "", "订单表：每一行是一笔客户订单，含金额与状态；按租户隔离。", []string{"订单", "sales orders"}, nil)
	up("customers", "", "客户主数据表：每一行是一个客户。", []string{"客户", "clients"}, nil)
	// orders columns.
	up("orders", "amount", "订单金额，单位人民币元。", []string{"金额", "成交额", "revenue"}, []string{"120.50", "340.00"})
	up("orders", "status", "订单状态。", []string{"状态"}, []string{"paid", "open", "refunded"})
	up("orders", "customer", "下单客户名称（冗余存储，便于展示）。", []string{"客户名"}, []string{"Acme Corp"})
	up("orders", "tenant_id", "租户标识，用于多租户行级隔离（由平台自动过滤，无需手写）。", []string{"租户"}, []string{"acme", "globex"})
	// customers columns.
	up("customers", "name", "客户名称。", []string{"名称", "客户名"}, []string{"Acme Corp"})
	up("customers", "phone", "联系电话（对受限角色动态掩码，如 138****5678）。", []string{"手机", "电话"}, []string{"13812345678"})
	up("customers", "email", "联系邮箱（对受限角色动态掩码，如 o***@acme.com）。", []string{"邮箱"}, []string{"ops@acme.com"})
	up("customers", "tenant_id", "租户标识（由平台自动过滤）。", []string{"租户"}, nil)
}

func datasourceIDByName(st *store.Store, name string) (string, error) {
	all, err := st.ListDataSources(context.Background())
	if err != nil {
		return "", err
	}
	for _, d := range all {
		if d.Name == name {
			return d.ID, nil
		}
	}
	return "", nil
}

// buildDemoDB creates a SQLite database with two tables and seed rows spanning
// two tenants, so row-level policies are observable.
func buildDemoDB(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()

	schema := []string{
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			customer TEXT,
			amount REAL,
			status TEXT)`,
		`CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT,
			phone TEXT,
			email TEXT)`,
		`INSERT INTO orders (tenant_id, customer, amount, status) VALUES
			('acme','Acme Corp',120.50,'paid'),
			('globex','Globex Inc',340.00,'paid'),
			('acme','Acme Retail',75.25,'open'),
			('initech','Initech',12.00,'refunded')`,
		`INSERT INTO customers (tenant_id, name, phone, email) VALUES
			('acme','Acme Corp','13812345678','ops@acme.com'),
			('globex','Globex Inc','13900001111','contact@globex.com'),
			('initech','Initech','13722223333','hi@initech.com')`,
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("seed demo: %w", err)
		}
	}
	return nil
}
