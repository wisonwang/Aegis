---
name: fyc-order-analysis
description: 复游会（Fuyouhui）订单/GMV 分析专用 skill。当用户需要分析复游会订单数据时使用——例如「复游会 GMV 多少」「各渠道/品牌/订单状态分布」「退款退订分析」「按年/月趋势」「目的地分布」「业务分类 L1/L2/L3 贡献」等。通过 Aegis 治理网关的 MCP 服务（客户端配置的 `aegis` MCP server）查询 Presto 数据源 `presto` 上的订单明细表 `dws_order_gmv_detail_2021`。内嵌已验证的口径（GMV 用 `gmv_pricetotal`）、维度取值目录、可直接复用的分析 SQL 模板、数据质量陷阱与治理约束。仅在用户提到「复游会 / fyc / 订单 / GMV / 渠道 / 品牌 / 退款」且数据来源为 Aegis 治理的 Presto 时使用。
agent_created: true
---

# 复游会订单分析（Aegis 治理网关 MCP）

## Overview
复游会（Fuyouhui，内部缩写 fyc）是复星旅文旗下的会员/订单业务。其订单 GMV 明细落在 Presto（底层 StarRocks/Hive）宽表 `dws_order_gmv_detail_2021` 上，通过 Aegis 数据治理网关的 MCP 服务对外提供**受治理、默认拒绝**的查询能力。本 skill 封装该表的分析方法论：关键维度、GMV 口径、可直接复用的 SQL 模板、数据质量陷阱与治理约束，使 agent 能安全、准确地回答复游会订单类问题。

## When to use
- 用户问复游会订单/GMV 相关问题：「复游会 GMV 多少」「各渠道 GMV 分布」「品牌贡献」「订单状态构成」「退款/退订分析」「按年/月趋势」「目的地分布」「业务分类贡献」。
- 用户引用 `dws_order_gmv_detail_2021`、`presto` 数据源、或 `fyc` / `复游会`。
- Do NOT use for：写操作（Aegis MCP 仅只读）、或该表之外的其他数据源（除非用户明确指定别的 datasource）。

## Prerequisites
- 客户端已配置 `aegis` MCP server，指向 `http://localhost:8080/mcp`（见 `~/.workbuddy/mcp.json`）。认证用 `Authorization: Bearer <JWT>`（admin 或服务账号）。若工具调用返回 `unauthorized`，说明 JWT 已过期/签名密钥不匹配——需用当前实例 `jwt_secret` 重新签发（本地调试可用 `conf/config.local.json` 中的 `jwt_secret` 生成 admin token）。
- Aegis 中已注册 Presto 数据源，注册名 **`presto`**（类型 `presto`，下游 `catalog=hive&schema=dw_tc`）。本 skill 一律用 `datasource: "presto"` + `table: "dws_order_gmv_detail_2021"`；非限定表名即可命中（无需写 `hive.dw_tc.` 前缀）。
- 可用 MCP 工具：`list_datasources` / `list_tables` / `describe_table` / `get_catalog`（探查），`query`（执行受治理 SQL），`nl2sql`（自然语言转 SQL），`estimate_query`（执行前评估成本/风险），`list_metrics` / `run_metric`（若已注册指标）。

## Data dictionary（已 describe_table 验证，133 列精选）
**GMV / 金额口径（关键）**
- `gmv_pricetotal` decimal(38,3) — ★ 总金额（GMV 口径）。**所有 GMV 指标必须用此列**，不要混用 `pricetotal` / `pricepay` / `priceorder`。
- `pricetotal` / `pricepay` / `priceorder` decimal — 其他金额口径（非 GMV），仅特定场景使用。
- `refund_amount` decimal — 退款金额；`apply_bv` decimal — 申请金额。
- `fk_currency` varchar — 币种（报告未拆分，默认视为单一币种，跨币种分析需先 `GROUP BY fk_currency`）。

**维度（高价值）**
- `data_source` varchar — 订单系统/来源。**取值（7 类，已验证）**：`xiangminiao`(享咪鸟)、`silk`(奢旅)、`feizhu_api`(飞猪)、`fostay`、`shop`、`太仓度假区小程序`、`foto`。
- `order_status` varchar — 订单状态（27 种，主要见下「数据质量陷阱」）。
- `brand` varchar — 品牌（如 `三亚·亚特兰蒂斯`、`Club Med`、`托迈酷客`、`外部品牌`、`测试-排除组`、`复游拍`、`丽子拾`、`迷你营`；以 `GROUP BY brand` 取最新）。
- `order_date` varchar / `order_time` timestamp — 下单日期/时间。按年切分用 `substr(order_date,1,4)`。
- `cal_mon_cd` / `cal_week_cd` varchar — 日历月/周（趋势分析首选，比 `substr(order_date)` 更规范）。
- `destination_city` / `destination_province` / `destination_country` varchar — 目的地（地理分布分析）。
- `business_l1_2024` / `business_l2_2024` / `business_l3_2024` varchar — 2024 业务分类分层；`business_l1_2023` / `business_l2_2023` 为 2023 口径。另有 `product_business_type_name*`、`channel_classification*` 系列。
- `product_name` / `product_name_main` / `product_code` varchar、`product_num` bigint — 商品维度。
- `room_nights` integer — 间夜数（酒旅核心指标）。
- `adultnum` / `childnum` / `babynum` / `eldernum` bigint — 出行人数。
- `tcg_channel_name` / `tcg_team` / `tcg_seller`、`agent_name` / `agent_team_name`、`team_name`、`company_name` / `merchant_name` / `store_name` — 渠道/团队/商户归因。
- `utm_source` / `track_code` / `trackcode_name` / `source_type` / `apply_channel` / `promoting_way` / `promoting_channel` — 流量/推广归因。
- `activity_id` / `activity_name` — 活动归因。
- `is_distribution` / `is_employee` / `is_em_member` / `member_id` — 标识类。

**PII（治理可能脱敏/掩码，禁止直接外发明文）**
- `member_phone` / `ordercontacts_phone` / `reservation_phone` varchar、`member_id` varchar、`ordercontacts_name` / `reservation_name` varchar。分析时只用聚合（count/group by 非 PII 维度），切勿 SELECT 这些列返回给最终用户；若治理引擎已掩码，则直接采用掩码结果。

> 完整 133 列随时用 `describe_table(datasource:"presto", table:"dws_order_gmv_detail_2021")` 获取；本字典只列分析高频列。

## Curated analysis recipes（经 `query` 工具验证可用）
> 全部以 `datasource:"presto"` 执行。聚合查询会被治理引擎自动追加 `LIMIT 10000` 作为安全兜底（见治理约束）。

**R1 · 总规模（订单数 + GMV）**
```sql
SELECT count(*) AS cnt, sum(gmv_pricetotal) AS total_gmv FROM dws_order_gmv_detail_2021;
```

**R2 · 按订单系统（data_source）**
```sql
SELECT data_source, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021 GROUP BY data_source ORDER BY gmv DESC;
```

**R3 · 按订单状态（order_status）**
```sql
SELECT order_status, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021 GROUP BY order_status ORDER BY gmv DESC;
```

**R4 · 按年趋势（order_date 截取）**
```sql
SELECT substr(order_date,1,4) AS yr, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021 GROUP BY substr(order_date,1,4) ORDER BY yr;
-- 推荐改用日历维度：GROUP BY cal_mon_cd ORDER BY cal_mon_cd 看月度趋势
```

**R5 · 按品牌 Top（排除测试组）**
```sql
SELECT brand, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021
WHERE brand <> '测试-排除组'
GROUP BY brand ORDER BY gmv DESC LIMIT 12;
```

**R6 · 退款/退订分析**
```sql
-- 退订
SELECT count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021 WHERE order_status IN ('已退订','退订单');
-- 退款（含全额退款）
SELECT count(*) AS cnt, sum(gmv_pricetotal) AS gmv, sum(refund_amount) AS refund_amt
FROM dws_order_gmv_detail_2021 WHERE order_status IN ('已退款','全额退款');
-- 退款率（退订+退款 占全部）
SELECT
  sum(CASE WHEN order_status IN ('已退订','退订单','已退款','全额退款','取消单') THEN 1 ELSE 0 END)*1.0/count(*) AS refund_rate,
  sum(CASE WHEN order_status IN ('已退订','退订单','已退款','全额退款','取消单') THEN gmv_pricetotal ELSE 0 END)/sum(gmv_pricetotal) AS refund_gmv_rate
FROM dws_order_gmv_detail_2021;
```

**R7 · 按目的地（geo）**
```sql
SELECT destination_province, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021
WHERE destination_province IS NOT NULL AND destination_province <> ''
GROUP BY destination_province ORDER BY gmv DESC LIMIT 15;
```

**R8 · 按业务分类 L1/L2/L3（2024 口径）**
```sql
SELECT business_l1_2024, business_l2_2024, business_l3_2024,
       count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021
WHERE business_l1_2024 IS NOT NULL AND business_l1_2024 <> ''
GROUP BY business_l1_2024, business_l2_2024, business_l3_2024
ORDER BY gmv DESC LIMIT 20;
```

**R9 · Top 商品**
```sql
SELECT product_name_main, count(*) AS cnt, sum(gmv_pricetotal) AS gmv
FROM dws_order_gmv_detail_2021
WHERE product_name_main IS NOT NULL AND product_name_main <> ''
GROUP BY product_name_main ORDER BY gmv DESC LIMIT 20;
```

## Curated metrics（已注册 run_metric，优先于手写 SQL）
以下 5 个指标已注册到 Aegis 的 `presto` 数据源（`metric_definitions` 表，workspace=default），经治理引擎执行。调用方式：`run_metric(datasource:"presto", metric:"<name>", params:{...})`。**所有指标已内置 `brand <> '测试-排除组'` 护栏**；参数里日期用 `"2026-07-06 00:00:00"` 形式。

| 指标名 | 参数 | 返回字段 | 用途 |
|---|---|---|---|
| `fyc_period_overview` | `start`,`end`（date，含/不含） | `order_cnt`,`gmv`,`aov`,`room_nights` | 指定区间总览 |
| `fyc_by_channel` | `start`,`end` | 按 `data_source`：单量/GMV/AOV/退款额/退款率 | 订单系统来源对比 |
| `fyc_by_channel_classification_map` | `start`,`end` | 按 `channel_classification_map`：同上 | 渠道业务归类对比 |
| `fyc_by_biz_l3_2024` | `start`,`end` | 按 `business_l3_2024`：同上 | 品类结构（门票/住宿/出境） |
| `fyc_7d_wow_yoy` | `as_of`（date，默认 `2026-08-05`） | 按 `channel_classification_map` 的 `cur_*`/`prev_*`/`yoy_*` 单量与 GMV | 近7天 + 上周同期(WoW) + 去年同期(YoY) 三窗口，调用方自行算同比% |

**指标引擎已知坑（务必遵守）**：指标 SQL 模板里的日期参数必须用 `timestamp :param` 形式（渲染为 `timestamp '2026-07-06 00:00:00'`）。用 `CAST(:param AS date)` 会在 metric 渲染路径报错 `Column 'date' cannot be resolved`——这是 Aegis metric 引擎的渲染限制，不是 Trino 语法问题（同一条 `CAST(... AS date)` 在 `query` 工具里能跑）。区间参数 `start`/`end` 语义：`>= start AND < end`（end 不含）。

**典型调用示例**
```jsonc
// 近一个月总览
run_metric(datasource:"presto", metric:"fyc_period_overview",
           params:{ "start":"2026-07-06 00:00:00", "end":"2026-08-06 00:00:00" })
// 近7天 量额三窗口（按渠道归类）
run_metric(datasource:"presto", metric:"fyc_7d_wow_yoy", params:{ "as_of":"2026-08-05" })
```

## Known baseline（验证查询时快照，数据随源更新，请重新跑 R1 取当期值）
- 订单数 ≈ **726,560**，GMV（`gmv_pricetotal` 求和）≈ **607,550,722**（≈ 6.08 亿元，单位疑为元）。
- 按渠道 GMV：xiangminiao ≈ 3.76 亿（55.4 万单）、silk ≈ 1.47 亿、feizhu_api ≈ 0.39 亿、fostay ≈ 0.37 亿、shop ≈ 0.05 亿、太仓度假区小程序 ≈ 0.03 亿、foto ≈ 0.007 亿。
- 按状态 GMV（Top）：已完成 ≈ 2.49 亿、确认单 ≈ 1.26 亿、已使用 ≈ 0.98 亿、已退订 ≈ 0.66 亿。
> 注意：2026-07-30 的探查曾记录 713,040 单 / 6.01 亿，与本次（726,560 单 / 6.08 亿）略有差异，说明源表持续更新。**不要硬编码这些数字**，把它们当 sanity-check，正式回答前重跑 R1。

## Data quality guardrails（MUST，漏掉会口径错误）
1. **测试数据排除**：`brand = '测试-排除组'`（数百~上千单、数百万 GMV）为测试组，**经营分析必须 `WHERE brand <> '测试-排除组'`**。
2. **`外部品牌` 兜底口径**：`brand = '外部品牌'` 是原始品牌为空/未归并的兜底类（数万单、上亿 GMV），分析占比时单独标注，勿与真实品牌并列解读。
3. **`foto` 渠道状态为空**：`data_source = 'foto'` 的订单 `order_status` 全为 NULL（约 9,769 单），不要把它当成「未知状态」混入状态分布；状态维度分析可注明或 `WHERE order_status IS NOT NULL`。
4. **GMV 口径唯一**：只认 `gmv_pricetotal`，不要用 `pricetotal`/`pricepay` 当 GMV。
5. **币种**：`fk_currency` 未拆分，默认单一币种；若需多币种，先 `GROUP BY fk_currency` 再折算。
6. **PII**：`member_phone`/`ordercontacts_phone`/`reservation_phone`/`member_id` 等为个人信息，禁止 SELECT 明文返回最终用户；只用聚合。
7. **`business_l3_2024` 缺失分类 = NULL，不是字面「未分类」**：表里 **没有** 字面字符串 `'未分类'`（只有 `'待明确'` 这类真值）。`business_l3_2024` 为 NULL 的订单是「业务三级分类主数据未配」的缺口——展示/脚本常把它渲染成「未分类」占位符，但 SQL 必须用 `IS NULL` 过滤（用 `= '未分类'` 会返回 0 行）。近一月（2026-07-06~08-06）NULL 桶 = **435 单 / ¥160.9 万 / AOV ¥3,699**，且 100% 来自 `xiangminiao`（享咪鸟）× 品牌「三亚·亚特兰蒂斯」的住宿/房券产品（L2 已归「海南其他-向蜜鸟酒店」、退款率 21.6%，与「亚特日历房」业态一致）。**本质是数据治理缺口而非异常订单**：补全 L3 主数据即可归入「住宿」大类。全表累计 NULL 高达 **20,137 单 / ¥6,425 万**，属普遍分类缺失，分析占比时单独列出，勿与正常品类并列。

## Standard workflow
1. **探查**：`list_datasources` 确认 `presto` 在线；首次或列变更时用 `describe_table(presto, dws_order_gmv_detail_2021)` 或 `get_catalog` 核对列名/口径（列多，先确认再写 SQL）。
2. **自然语言问题** → 调 `nl2sql`（可先 `get_catalog` 提升命中）；返回 `generated_sql` + `explanation` + 结果。
3. **已知 SQL** → 调 `query`，返回 `rewritten_sql`（实际执行 SQL，含注入的 LIMIT）与 `queryResult`。**把 `rewritten_sql` 透出给用户**以示透明。
4. **大表/PII/广扫描前** → 先 `estimate_query` 看预估扫描行、触碰的敏感列、风险等级（low/medium/high）；高风险则收紧 `WHERE` 或缩小范围。
5. **KPI** → 若 Aegis 已注册 `fyc_*` 指标，优先 `list_metrics` + `run_metric`，胜过手写 SQL。
6. **应用护栏**：在 WHERE 中默认排除 `测试-排除组`；GMV 只用 `gmv_pricetotal`；结果截断遵循 `MaxRows`。

## Governance notes
- 所有执行路径经治理引擎 default-deny 重写：表/行/列权限 + 值掩码，调用方（analyst）只见被授权部分；admin 见更多。
- `query` / `nl2sql` **只读**；写操作走 DataAPI，不走 MCP。
- **SQL 层 LIMIT 注入**：聚合/未带 LIMIT 的查询会被追加 `LIMIT 10000`（ADR-0003 P0+1）——已验证（rewritten_sql 含 `limit 10000`）。结果集过大再被 `MaxRows`(10000) 截断。
- 行策略/列脱敏由 Aegis 强制，agent 无法绕过；`rewritten_sql` 即真实执行的语句。

## References
- `references/fyc-dictionary.md` — 维度取值目录与高频列速查（从 live describe_table 抽取）。
- 通用 Aegis MCP 用法见 `aegis-mcp` skill（`/Users/vincent/workspace/fosun/datahub/aegis-mcp/SKILL.md`）。
