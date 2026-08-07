package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/proxy"
	"github.com/wisonwang/aegis/internal/store"
	_ "modernc.org/sqlite"
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

	// Demo datasource. When the local control plane runs on MySQL, reuse that
	// DSN as the seeded demo datasource so the admin UI immediately shows a
	// MySQL-backed demo instead of the default sqlite file.
	demoType, demoDSN, err := seedDemoDatasource(cfg)
	if err != nil {
		return err
	}
	_ = st.CreateDataSource(context.Background(), &store.DataSource{Name: "demo", Type: demoType, DSN: demoDSN})

	dsID, err := datasourceIDByName(st, "demo")
	if err != nil || dsID == "" {
		return fmt.Errorf("demo datasource not found after creation")
	}

	// Governance: analyst may SELECT hotel operations tables; both facts and guest
	// profiles are scoped to the caller tenant.
	_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "hotel_bookings", Ops: "SELECT",
	})
	_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "guest_profiles", Ops: "SELECT",
	})
	_ = st.CreateRowPolicy(context.Background(), &store.RowPolicy{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "hotel_bookings",
		Predicate: "tenant_id = :tenant", Priority: 10,
	})
	_ = st.CreateRowPolicy(context.Background(), &store.RowPolicy{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "guest_profiles",
		Predicate: "tenant_id = :tenant", Priority: 10,
	})

	// Dynamic masking: the analyst keeps guest contact PII visible but masked, so
	// AI supply stays useful without leaking personal data.
	_ = st.UpsertColumnMask(context.Background(), &store.ColumnMask{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "guest_profiles",
		ColumnName: "phone", Strategy: "phone",
	})
	_ = st.UpsertColumnMask(context.Background(), &store.ColumnMask{
		RoleID: analyst.ID, DataSourceID: dsID, TableName: "guest_profiles",
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

	// Dataset management demo: a curated hotel operations board over the demo
	// source. The analyst may consume it without touching raw booking tables.
	_ = st.CreateDataset(context.Background(), &store.Dataset{
		Name:         "hotel_confirmed_bookings",
		DisplayName:  "已确认订单经营看板",
		Description:  "已确认或在住订单的数据产品，沉淀酒店、渠道、房型、间夜和房费口径，按租户隔离。",
		DataSourceID: dsID,
		Definition:   "SELECT id, tenant_id, hotel_name, biz_date, channel, room_type, guest_name, guest_count, room_nights, room_revenue, total_revenue, booking_status FROM hotel_bookings WHERE booking_status IN ('confirmed', 'checked_in')",
		Status:       store.DatasetPublished,
		Fields:       `[{"name":"id","type":"integer"},{"name":"tenant_id","type":"text"},{"name":"hotel_name","type":"text"},{"name":"biz_date","type":"text"},{"name":"channel","type":"text"},{"name":"room_type","type":"text"},{"name":"guest_name","type":"text"},{"name":"guest_count","type":"integer"},{"name":"room_nights","type":"integer"},{"name":"room_revenue","type":"real"},{"name":"total_revenue","type":"real"},{"name":"booking_status","type":"text"}]`,
	})
	pd, _ := st.GetDatasetByName(context.Background(), "hotel_confirmed_bookings")
	if pd != nil {
		_ = st.CreateTablePermission(context.Background(), &store.TablePermission{
			RoleID: analyst.ID, DataSourceID: dsID, TableName: "hotel_confirmed_bookings", Ops: "SELECT",
		})
		_ = st.CreateRowPolicy(context.Background(), &store.RowPolicy{
			RoleID: analyst.ID, DataSourceID: dsID, TableName: "hotel_confirmed_bookings",
			Predicate: "tenant_id = :tenant", Priority: 10,
		})
		// Dataset-level semantics enrich the catalog the agent sees.
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: "hotel_confirmed_bookings", ColumnName: "room_revenue",
			Description: "房费收入，单位人民币元，仅包含已确认或在住订单。",
			Synonyms:    `["房费","房费收入","room revenue"]`,
			Examples:    `["1480.00","2680.00"]`,
		})
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: "hotel_confirmed_bookings", ColumnName: "hotel_name",
			Description: "订单归属酒店名称。", Synonyms: `["酒店","门店"]`,
		})
		_ = st.UpsertSemantic(context.Background(), &store.Semantic{
			DataSourceID: dsID, TableName: "hotel_confirmed_bookings", ColumnName: "channel",
			Description: "订单来源渠道，如 OTA、官网直销、会员中心、企业协议。", Synonyms: `["渠道","来源渠道"]`,
		})
	}
	return nil
}

// reconcileDemoDatasource keeps the local demo datasource aligned with the
// active local backend. This lets an existing dev control plane transparently
// switch its seeded `demo` datasource from sqlite to mysql after config changes.
func reconcileDemoDatasource(st *store.Store, cfg *config.Config) error {
	if cfg.DBType != "mysql" {
		return nil
	}
	ds, err := demoDatasourceByName(st, "demo")
	if err != nil || ds == nil {
		return err
	}
	if ds.Type == "mysql" && ds.DSN == cfg.DBDSN {
		return nil
	}
	if err := buildDemoMySQL(cfg.DBDSN); err != nil {
		return err
	}
	ds.Type = "mysql"
	ds.DSN = cfg.DBDSN
	return st.UpdateDataSource(ds)
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
	// Table-level marks: the guest table holds PII, the booking fact table holds
	// both financial and operational hotel data.
	up("guest_profiles", "", "restricted", []string{"pii", "guest"})
	up("hotel_bookings", "", "confidential", []string{"financial", "hotel_ops"})
	// guest_profiles columns carry direct personal identifiers.
	up("guest_profiles", "guest_name", "pii", []string{"pii:name", "guest"})
	up("guest_profiles", "phone", "pii", []string{"pii:phone", "contact"})
	up("guest_profiles", "email", "pii", []string{"pii:email", "contact"})
	// hotel_bookings revenue columns are financial figures.
	up("hotel_bookings", "room_revenue", "confidential", []string{"financial", "money"})
	up("hotel_bookings", "fnb_revenue", "confidential", []string{"financial", "money"})
	up("hotel_bookings", "total_revenue", "confidential", []string{"financial", "money"})
	// The published dataset inherits the same sensitivity for its room revenue.
	up("hotel_confirmed_bookings", "guest_name", "pii", []string{"pii:name", "guest"})
	up("hotel_confirmed_bookings", "room_revenue", "confidential", []string{"financial", "money"})
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
	up("hotel_bookings", "", "酒店订单事实表：记录每日经营订单，覆盖酒店、城市、渠道、房型、间夜和收入。", []string{"订单事实", "预订明细", "bookings"}, nil)
	up("guest_profiles", "", "住客画像表：记录住客会员等级、来源渠道和联系方式。", []string{"住客", "客史", "guest profiles"}, nil)
	// hotel_bookings columns.
	up("hotel_bookings", "hotel_name", "订单归属酒店名称。", []string{"酒店", "门店"}, []string{"三亚海棠湾度假酒店"})
	up("hotel_bookings", "city", "酒店所在城市。", []string{"城市"}, []string{"三亚", "上海"})
	up("hotel_bookings", "biz_date", "经营日，用于晨会看板与日报口径。", []string{"经营日", "business date"}, []string{"2026-08-03"})
	up("hotel_bookings", "channel", "订单来源渠道，如 OTA、官网直销、企业协议。", []string{"渠道", "来源渠道"}, []string{"OTA", "Direct", "Corporate"})
	up("hotel_bookings", "room_type", "预订房型。", []string{"房型"}, []string{"海景套房", "亲子房"})
	up("hotel_bookings", "booking_status", "订单状态，常见值为 confirmed、checked_in、pending、cancelled。", []string{"订单状态", "入住状态"}, []string{"confirmed", "checked_in"})
	up("hotel_bookings", "guest_name", "主入住人姓名（冗余存储，便于经营分析展示）。", []string{"客人姓名", "入住人"}, []string{"Alice Zhang"})
	up("hotel_bookings", "guest_count", "订单对应的住客人数。", []string{"住客数", "入住人数"}, []string{"2", "3"})
	up("hotel_bookings", "room_nights", "订单对应间夜数。", []string{"间夜", "room nights"}, []string{"1", "2"})
	up("hotel_bookings", "room_revenue", "房费收入，单位人民币元。", []string{"房费", "房费收入", "room revenue"}, []string{"1480.00", "2680.00"})
	up("hotel_bookings", "fnb_revenue", "餐饮及附加消费收入，单位人民币元。", []string{"餐饮收入", "附加收入"}, []string{"280.00", "660.00"})
	up("hotel_bookings", "total_revenue", "订单总收入，含房费与餐饮等。", []string{"总收入", "订单收入"}, []string{"1760.00", "3160.00"})
	up("hotel_bookings", "tenant_id", "租户标识，用于多租户行级隔离（由平台自动过滤，无需手写）。", []string{"租户"}, []string{"acme", "globex"})
	// guest_profiles columns.
	up("guest_profiles", "guest_name", "住客姓名。", []string{"客人姓名", "入住人"}, []string{"Alice Zhang"})
	up("guest_profiles", "hotel_name", "最近入住或意向酒店。", []string{"常住酒店", "偏好酒店"}, []string{"三亚海棠湾度假酒店"})
	up("guest_profiles", "member_tier", "会员等级，如 silver、gold、platinum。", []string{"会员等级", "会员层级"}, []string{"gold", "platinum"})
	up("guest_profiles", "phone", "联系电话（对受限角色动态掩码，如 138****5678）。", []string{"手机", "电话"}, []string{"13812345678"})
	up("guest_profiles", "email", "联系邮箱（对受限角色动态掩码，如 a***@demo.com）。", []string{"邮箱"}, []string{"alice.zhang@demo.com"})
	up("guest_profiles", "tenant_id", "租户标识（由平台自动过滤）。", []string{"租户"}, nil)
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

func demoDatasourceByName(st *store.Store, name string) (*store.DataSource, error) {
	all, err := st.ListDataSources(context.Background())
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.Name == name {
			return d, nil
		}
	}
	return nil, nil
}

func seedDemoDatasource(cfg *config.Config) (string, string, error) {
	if cfg.DBType == "mysql" {
		if err := buildDemoMySQL(cfg.DBDSN); err != nil {
			return "", "", err
		}
		return "mysql", cfg.DBDSN, nil
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return "", "", err
	}
	demoPath := filepath.Join(cfg.DataDir, "demo.db")
	if err := buildDemoSQLite(demoPath); err != nil {
		return "", "", err
	}
	return "sqlite", demoPath, nil
}

// buildDemoSQLite creates a SQLite database with hotel-operation demo tables and
// seed rows spanning multiple tenants, so row-level policies are observable.
func buildDemoSQLite(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()

	schema := demoSchema("sqlite")
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("seed demo: %w", err)
		}
	}
	return nil
}

// buildDemoMySQL creates/refreshes the demo tables inside the configured MySQL
// schema so a local MySQL control plane also surfaces a MySQL demo datasource.
func buildDemoMySQL(dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return err
	}

	schema := demoSchema("mysql")
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("seed demo: %w", err)
		}
	}
	return nil
}

func demoSchema(kind string) []string {
	switch kind {
	case "mysql":
		return []string{
			`DROP TABLE IF EXISTS hotel_bookings`,
			`DROP TABLE IF EXISTS guest_profiles`,
			`CREATE TABLE guest_profiles (
				id BIGINT PRIMARY KEY AUTO_INCREMENT,
				tenant_id VARCHAR(64) NOT NULL,
				hotel_name VARCHAR(255),
				city VARCHAR(128),
				guest_name VARCHAR(255),
				phone VARCHAR(64),
				email VARCHAR(255),
				member_tier VARCHAR(64),
				preferred_channel VARCHAR(64)
			)`,
			`CREATE TABLE hotel_bookings (
				id BIGINT PRIMARY KEY AUTO_INCREMENT,
				tenant_id VARCHAR(64) NOT NULL,
				hotel_code VARCHAR(64),
				hotel_name VARCHAR(255),
				city VARCHAR(128),
				biz_date DATE,
				check_in_date DATE,
				check_out_date DATE,
				channel VARCHAR(64),
				room_type VARCHAR(128),
				booking_status VARCHAR(64),
				guest_name VARCHAR(255),
				guest_count INT,
				room_nights INT,
				room_revenue DECIMAL(12,2),
				fnb_revenue DECIMAL(12,2),
				total_revenue DECIMAL(12,2)
			)`,
			`INSERT INTO hotel_bookings (tenant_id, hotel_code, hotel_name, city, biz_date, check_in_date, check_out_date, channel, room_type, booking_status, guest_name, guest_count, room_nights, room_revenue, fnb_revenue, total_revenue) VALUES
				('acme','SY-HTW','三亚海棠湾度假酒店','三亚','2026-08-03','2026-08-03','2026-08-05','OTA','海景套房','confirmed','Alice Zhang',2,2,2680.00,480.00,3160.00),
				('globex','SH-BUND','上海外滩城市酒店','上海','2026-08-03','2026-08-03','2026-08-04','Direct','行政套房','checked_in','Brian Chen',1,1,1880.00,320.00,2200.00),
				('acme','HZ-WEST','杭州西湖度假酒店','杭州','2026-08-03','2026-08-03','2026-08-04','Member','园景大床房','checked_in','Daisy Wang',1,1,1480.00,280.00,1760.00),
				('acme','SY-HTW','三亚海棠湾度假酒店','三亚','2026-08-03','2026-08-04','2026-08-06','Corporate','亲子房','pending','Cathy Li',3,2,2360.00,660.00,3020.00),
				('initech','XM-ISLAND','厦门海景酒店','厦门','2026-08-03','2026-08-05','2026-08-06','OTA','标准房','cancelled','Eric Xu',2,1,0.00,0.00,0.00)`,
			`INSERT INTO guest_profiles (tenant_id, hotel_name, city, guest_name, phone, email, member_tier, preferred_channel) VALUES
				('acme','三亚海棠湾度假酒店','三亚','Alice Zhang','13812345678','alice.zhang@demo.com','gold','OTA'),
				('globex','上海外滩城市酒店','上海','Brian Chen','13900001111','brian.chen@demo.com','platinum','Direct'),
				('acme','杭州西湖度假酒店','杭州','Daisy Wang','13722223333','daisy.wang@demo.com','silver','Member'),
				('initech','厦门海景酒店','厦门','Eric Xu','13688889999','eric.xu@demo.com','silver','OTA')`,
		}
	default:
		return []string{
			`CREATE TABLE hotel_bookings (
				id INTEGER PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				hotel_code TEXT,
				hotel_name TEXT,
				city TEXT,
				biz_date TEXT,
				check_in_date TEXT,
				check_out_date TEXT,
				channel TEXT,
				room_type TEXT,
				booking_status TEXT,
				guest_name TEXT,
				guest_count INTEGER,
				room_nights INTEGER,
				room_revenue REAL,
				fnb_revenue REAL,
				total_revenue REAL)`,
			`CREATE TABLE guest_profiles (
				id INTEGER PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				hotel_name TEXT,
				city TEXT,
				guest_name TEXT,
				phone TEXT,
				email TEXT,
				member_tier TEXT,
				preferred_channel TEXT)`,
			`INSERT INTO hotel_bookings (tenant_id, hotel_code, hotel_name, city, biz_date, check_in_date, check_out_date, channel, room_type, booking_status, guest_name, guest_count, room_nights, room_revenue, fnb_revenue, total_revenue) VALUES
				('acme','SY-HTW','三亚海棠湾度假酒店','三亚','2026-08-03','2026-08-03','2026-08-05','OTA','海景套房','confirmed','Alice Zhang',2,2,2680.00,480.00,3160.00),
				('globex','SH-BUND','上海外滩城市酒店','上海','2026-08-03','2026-08-03','2026-08-04','Direct','行政套房','checked_in','Brian Chen',1,1,1880.00,320.00,2200.00),
				('acme','HZ-WEST','杭州西湖度假酒店','杭州','2026-08-03','2026-08-03','2026-08-04','Member','园景大床房','checked_in','Daisy Wang',1,1,1480.00,280.00,1760.00),
				('acme','SY-HTW','三亚海棠湾度假酒店','三亚','2026-08-03','2026-08-04','2026-08-06','Corporate','亲子房','pending','Cathy Li',3,2,2360.00,660.00,3020.00),
				('initech','XM-ISLAND','厦门海景酒店','厦门','2026-08-03','2026-08-05','2026-08-06','OTA','标准房','cancelled','Eric Xu',2,1,0.00,0.00,0.00)`,
			`INSERT INTO guest_profiles (tenant_id, hotel_name, city, guest_name, phone, email, member_tier, preferred_channel) VALUES
				('acme','三亚海棠湾度假酒店','三亚','Alice Zhang','13812345678','alice.zhang@demo.com','gold','OTA'),
				('globex','上海外滩城市酒店','上海','Brian Chen','13900001111','brian.chen@demo.com','platinum','Direct'),
				('acme','杭州西湖度假酒店','杭州','Daisy Wang','13722223333','daisy.wang@demo.com','silver','Member'),
				('initech','厦门海景酒店','厦门','Eric Xu','13688889999','eric.xu@demo.com','silver','OTA')`,
		}
	}
}
