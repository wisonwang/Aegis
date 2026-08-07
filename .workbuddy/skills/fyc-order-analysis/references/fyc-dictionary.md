# 复游会订单表 数据字典速查（fyc-order-analysis）

> 来源：Aegis MCP `describe_table(datasource:"presto", table:"dws_order_gmv_detail_2021")`（live，2026-08-06）。
> 表共约 133 列；下方为分析高频列，按主题分组。完整列随时 `describe_table` 复取。

## 一、维度取值目录（已验证，分析前先核对）
**data_source（订单系统，7 类）**
| 值 | 含义 | 备注 |
|---|---|---|
| xiangminiao | 享咪鸟 | GMV 主力（~3.76 亿） |
| silk | 奢旅 | ~1.47 亿 |
| feizhu_api | 飞猪 | ~0.39 亿 |
| fostay | — | ~0.37 亿 |
| shop | 商城 | ~0.05 亿 |
| 太仓度假区小程序 | 太仓度假区 | ~0.03 亿 |
| foto | — | 状态全 NULL（见护栏） |

**order_status（订单状态，27 种，经营分析高频）**
已完成 / 已使用 / 确认单 / 已退订 / 已结算 / 退订单 / 已退款 / 已支付 / 取消单 / 全额退款 / 已确认 / 待确认 / (NULL，仅 foto)

**brand（品牌，示例，以 `GROUP BY brand` 取最新）**
三亚·亚特兰蒂斯 / 外部品牌(兜底) / Club Med / 托迈酷客 / 测试-排除组(测试) / 复游拍 / 丽子拾 / 迷你营

**business_l*_2024（2024 业务分类 L1/L2/L3）** — 业务归因首选；另有 `*_2023` 历史口径、`product_business_type_name*` / `channel_classification*` 系列。

## 二、列索引（按主题）
**标识 / 时间**
`order_id`(varchar) · `order_date`(varchar) · `order_time`(timestamp) · `cal_mon_cd`(varchar,日历月) · `cal_week_cd`(varchar,日历周) · `createtime`/`modifytime`(timestamp) · `dt`/`is_today_data`

**GMV / 金额**
`gmv_pricetotal`(decimal ★GMV口径) · `pricetotal` · `pricepay` · `priceorder` · `refund_amount`(decimal) · `apply_bv`(decimal) · `fk_currency`(varchar 币种)

**渠道 / 来源**
`data_source` · `channel_name` · `channel_class` · `channel_classification`(+`_strategic`/`_map`) · `tcg_channel_name`/`tcg_team`/`tcg_seller` · `utm_source` · `track_code`/`trackcode_name` · `source_type` · `apply_channel` · `promoting_way`/`promoting_channel` · `type_origin` · `businesstag` · `platform`

**品牌 / 商品**
`brand` · `product_id`/`product_code`/`product_code_from` · `product_name`/`product_name_main`/`product_name_subhead` · `product_type` · `product_num`(bigint) · `product_business_type_name`(+`_strategic`/`_big`/`_chairman`) · `new_product_business_type_name*`

**业务分类 / 组织**
`business_l1_2024`/`business_l2_2024`/`business_l3_2024` · `business_l1_2023`/`business_l2_2023` · `own_project`/`own_depart` · `company_name`/`merchant_name`/`store_name`/`code_company_name` · `sub_company_name` · `operator_group`/`operator_channel_1..3` · `team_id`/`team_name` · `depart_name`

**订单状态 / 生命周期**
`order_status` · `paymentstatus` · `fk_paymentmode`/`fk_paymentmode_name` · `contract_status` · `type_order` · `is_distribution` · `tb_order_type`
退订：`unsubscribe_id`/`unsubscribe_status`/`unsubscribe_type`
退款：`refund_id`/`refund_status`/`refund_type`/`refund_amount`/`refundtime`/`refund_paytime`/`refund_confirmtime`/`store_refundtime`
时间节点：`confirmtime`/`customer_confirmation_time`/`canceltime`/`appointmentapplytime`/`processtime`/`max_date`/`min_date`/`apply_min_time`/`apply_max_time`

**客户 / PII（禁止明文返回）**
`member_id` · `member_phone` · `ordercontacts_name`/`ordercontacts_phone` · `reservation_name`/`reservation_phone` · `is_em_member`/`is_employee`/`employee_name`

**出行 / 目的地**
`destination_city`/`destination_province`/`destination_country` · `departuredate`/`arrivaldate`(timestamp) · `adultnum`/`childnum`/`babynum`/`eldernum`(bigint) · `room_nights`(integer 间夜)

**代理 / 活动 / 供应商**
`agent_id`/`agent_name`/`agent_team_name`/`agent_company` · `track_agent_id`/`track_agent_name` · `activity_id`/`activity_name` · `supplier_id`/`supplier` · `coupon_id`/`coupon_name` · `pm`/`fk_user_process`

**其他**
`sn`/`trade_sn` · `terminals` · `apply_num`/`apply_product_num`(bigint) · `product_corptition` · `usercat_cxs` · `tag46`/`tag46_biz` · `label_code`/`label_name` · `remain_tag` · `ext_info`(map) · `customize_data` · `product_business_type_name_map`/`tcg_maps`/`tcg_maps_before`

## 三、常用口径 SQL 片段
- 年：`substr(order_date,1,4)` 或规范日历 `cal_mon_cd`(月)/`cal_week_cd`(周)
- 退款率：`order_status IN ('已退订','退订单','已退款','全额退款','取消单')`
- 排除测试：`WHERE brand <> '测试-排除组'`
- 有效订单（排除空状态来源）：`WHERE order_status IS NOT NULL`
