package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fosun/aegis/internal/auth"
	"github.com/fosun/aegis/internal/config"
	"github.com/fosun/aegis/internal/store"
	_ "modernc.org/sqlite"
)

// seedIfEmpty populates a self-contained demo tenant the first time the
// platform boots with an empty control plane. It demonstrates the three core
// capabilities: centralized auth, table/row/column governance, and a ready
// datasource for the DataAPI and MCP endpoints.
func seedIfEmpty(st *store.Store, cfg *config.Config) error {
	users, err := st.ListUsers()
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return nil
	}

	// Roles
	_ = st.CreateRole(&store.Role{Name: "admin", Description: "平台管理员（超级用户，绕过行级治理）"})
	_ = st.CreateRole(&store.Role{Name: "analyst", Description: "数据分析师（受表/行/列权限约束）"})

	// Users
	adminHash, _ := auth.HashPassword("admin123")
	_ = st.CreateUser(&store.User{Username: "admin", DisplayName: "Administrator", PasswordHash: adminHash})
	analystHash, _ := auth.HashPassword("analyst123")
	_ = st.CreateUser(&store.User{Username: "analyst", DisplayName: "Analyst", PasswordHash: analystHash, Attributes: `{"tenant":"acme"}`})
	mcpHash, _ := auth.HashPassword("mcp123")
	_ = st.CreateUser(&store.User{Username: "mcp-agent", DisplayName: "MCP Agent", PasswordHash: mcpHash, Attributes: `{"tenant":"acme"}`})

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
	_ = st.CreateDataSource(&store.DataSource{Name: "demo", Type: "sqlite", DSN: demoPath})

	dsID, err := datasourceIDByName(st, "demo")
	if err != nil || dsID == "" {
		return fmt.Errorf("demo datasource not found after creation")
	}

	// Governance: analyst may SELECT orders/customers; orders is row-scoped to :tenant.
	_ = st.CreateTablePermission(&store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "orders", Ops: "SELECT",
	})
	_ = st.CreateTablePermission(&store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "customers", Ops: "SELECT",
	})
	_ = st.CreateRowPolicy(&store.RowPolicy{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "orders",
		Predicate: "tenant_id = :tenant", Priority: 10,
	})

	// Semantic layer: business descriptions so AI agents generate correct SQL.
	seedSemantics(st, dsID)
	return nil
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
		_ = st.UpsertSemantic(&store.Semantic{
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
	up("customers", "tenant_id", "租户标识（由平台自动过滤）。", []string{"租户"}, nil)
}

func datasourceIDByName(st *store.Store, name string) (string, error) {
	all, err := st.ListDataSources()
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
			name TEXT)`,
		`INSERT INTO orders (tenant_id, customer, amount, status) VALUES
			('acme','Acme Corp',120.50,'paid'),
			('globex','Globex Inc',340.00,'paid'),
			('acme','Acme Retail',75.25,'open'),
			('initech','Initech',12.00,'refunded')`,
		`INSERT INTO customers (tenant_id, name) VALUES
			('acme','Acme Corp'),
			('globex','Globex Inc'),
			('initech','Initech')`,
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("seed demo: %w", err)
		}
	}
	return nil
}
