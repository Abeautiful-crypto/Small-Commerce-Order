package store

import (
	"fmt"
	"strconv"
	"strings"

	"small-commerce-order/internal/model"
)

// demoProducts 是演示商品目录：覆盖家用电器 / 电子产品 / 儿童玩具 / 书本 / 食品 / 家居生活。
// 首次建库时整体写入；老库升级时由 ensureCatalog 幂等补齐缺失的条目。
// P1 的 available 已扣除预置订单占用的 1 件，其余可售 = 总库存。
func demoProducts() []*model.Product {
	return []*model.Product{
		{ID: "P1", Name: "无线蓝牙耳机 入耳式降噪 白", Image: "images/product1.png", Category: "电子产品", PriceFen: 10000, OldPriceFen: 19900, Total: 5, Available: 4, OnSale: true},
		{ID: "P2", Name: "坚果零食大礼包 每日坚果 红金礼盒", Image: "images/product2.png", Category: "食品", PriceFen: 10000, OldPriceFen: 15900, Total: 8, Available: 8, OnSale: true},
		{ID: "P3", Name: "智能电饭煲 4L 家用多功能", Image: "images/appliance.svg", Category: "家用电器", PriceFen: 29900, OldPriceFen: 39900, Total: 20, Available: 20, OnSale: true},
		{ID: "P4", Name: "轻薄笔记本电脑 14 英寸 16G", Image: "images/digital.svg", Category: "电子产品", PriceFen: 429900, OldPriceFen: 499900, Total: 10, Available: 10, OnSale: true},
		{ID: "P5", Name: "儿童益智积木 200 颗粒", Image: "images/toy.svg", Category: "儿童玩具", PriceFen: 8900, OldPriceFen: 12900, Total: 30, Available: 30, OnSale: true},
		{ID: "P6", Name: "儿童绘本套装 全 10 册", Image: "images/book.svg", Category: "书本", PriceFen: 6800, OldPriceFen: 9900, Total: 40, Available: 40, OnSale: true},
		{ID: "P7", Name: "空气炸锅 5L 无油低脂", Image: "images/appliance.svg", Category: "家用电器", PriceFen: 25900, OldPriceFen: 32900, Total: 18, Available: 18, OnSale: true},
		{ID: "P8", Name: "桌面蓝牙音箱 复古便携", Image: "images/digital.svg", Category: "电子产品", PriceFen: 15900, OldPriceFen: 21900, Total: 25, Available: 25, OnSale: true},
		{ID: "P9", Name: "遥控越野车 四驱耐摔", Image: "images/toy.svg", Category: "儿童玩具", PriceFen: 12900, OldPriceFen: 17900, Total: 22, Available: 22, OnSale: true},
		{ID: "P10", Name: "中国古典文学名著 4 册", Image: "images/book.svg", Category: "书本", PriceFen: 9800, OldPriceFen: 13900, Total: 35, Available: 35, OnSale: true},
		{ID: "P11", Name: "有机花草茶礼盒 6 味", Image: "images/food.svg", Category: "食品", PriceFen: 7900, OldPriceFen: 10900, Total: 28, Available: 28, OnSale: true},
		{ID: "P12", Name: "北欧风落地灯 暖光护眼", Image: "images/home.svg", Category: "家居生活", PriceFen: 19900, OldPriceFen: 26900, Total: 15, Available: 15, OnSale: true},
	}
}

// ensureCatalog 幂等升级商品目录：
//  1. 老库补 category 列与分类索引；
//  2. 回填已有商品的分类（只改分类，不动价格 / 库存 / 上下架）；
//  3. 补齐缺失的演示商品（INSERT IGNORE，绝不覆盖运营改过的数据）；
//  4. 抬高全局序号，避免新建商品与补齐的编号撞车。
func (s *MySQLStore) ensureCatalog() error {
	if err := s.ensureProductCategoryColumn(); err != nil {
		return err
	}
	// 老库升级：发货记录补签收列（签收记录由买家确认收货写入）
	if err := s.ensureColumn("shipments", "signed_at", "ALTER TABLE shipments ADD COLUMN signed_at DATETIME(3) NULL"); err != nil {
		return err
	}
	if err := s.ensureColumn("shipments", "signed_by", "ALTER TABLE shipments ADD COLUMN signed_by VARCHAR(128) NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// 老库升级：订单补支付方式与收货地址快照列（下单时写入）
	orderCols := []struct{ col, ddl string }{
		{"channel", "ALTER TABLE orders ADD COLUMN channel VARCHAR(16) NOT NULL DEFAULT ''"},
		{"receiver_name", "ALTER TABLE orders ADD COLUMN receiver_name VARCHAR(64) NOT NULL DEFAULT ''"},
		{"receiver_phone", "ALTER TABLE orders ADD COLUMN receiver_phone VARCHAR(24) NOT NULL DEFAULT ''"},
		{"receiver_address", "ALTER TABLE orders ADD COLUMN receiver_address VARCHAR(255) NOT NULL DEFAULT ''"},
	}
	for _, c := range orderCols {
		if err := s.ensureColumn("orders", c.col, c.ddl); err != nil {
			return err
		}
	}
	// 收货地址表（新库由迁移建表；老库幂等补建）
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS addresses (
		id          VARCHAR(64)  NOT NULL PRIMARY KEY,
		user_id     VARCHAR(64)  NOT NULL,
		name        VARCHAR(64)  NOT NULL,
		phone       VARCHAR(24)  NOT NULL,
		region      VARCHAR(128) NOT NULL,
		detail      VARCHAR(255) NOT NULL,
		is_default  TINYINT(1)   NOT NULL DEFAULT 0,
		created_at  DATETIME(3)  NOT NULL,
		INDEX idx_addresses_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='收货地址：每人最多 10 个、默认唯一，下单时快照进订单'`); err != nil {
		return fmt.Errorf("确保 addresses 表失败：%w", err)
	}
	for _, p := range demoProducts() {
		if _, err := s.db.Exec(`INSERT IGNORE INTO products
			(id,name,image,category,price_fen,old_price_fen,total,available,on_sale,sales)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, p.Image, p.Category, p.PriceFen, p.OldPriceFen,
			p.Total, p.Available, p.OnSale, p.Sales); err != nil {
			return fmt.Errorf("补齐演示商品 %s 失败：%w", p.ID, err)
		}
		// 分类为历史默认值时回填；运营已手工改过的分类保持不变
		if _, err := s.db.Exec(`UPDATE products SET category=? WHERE id=? AND (category='' OR category='其他')`,
			p.Category, p.ID); err != nil {
			return fmt.Errorf("回填商品 %s 分类失败：%w", p.ID, err)
		}
	}
	if err := s.ensureTableComments(); err != nil {
		return err
	}
	return s.raiseSeq()
}

// tableComments 是各表的中文说明，写入 MySQL 表注释（数据库工具「注释」列可直接看到）。
// 新库由 migrations/001_init.up.sql 的 COMMENT 建好；老库由 ensureTableComments 幂等补齐/覆盖。
var tableComments = []struct{ table, comment string }{
	{"products", "商品表：名称/分类/图片/单价(分)/总库存/可售库存/销量/上下架"},
	{"orders", "订单表：买家与商品价格快照、支付方式、收货地址快照、状态机与关单/付款时间"},
	{"payments", "收款记录：渠道交易号唯一，防重复收款与串单"},
	{"notify_results", "渠道支付通知处理结果：只增不改，用于对账与审计"},
	{"refunds", "退款单：售前/迟到来源、状态、客服审核与渠道重试、渠道退款号"},
	{"shipments", "发货记录：一单只发一次，签收时间与签收人由买家确认收货回写"},
	{"abnormal_payments", "异常收款登记：如迟到支付，记录处理结果与关联退款单"},
	{"ledger", "资金流水：收款/退款/退款审核等与钱相关动作，只增不改"},
	{"product_ops", "商品操作留痕：新增/上架/下架，与资金流水分开"},
	{"reviews", "商品评论与回复：parent_id 为空是一级评论，否则为回复"},
	{"chat_msgs", "订单客服消息：订单卡片(kind=order)与买卖双方文本"},
	{"addresses", "收货地址：每人最多 10 个、默认唯一，下单时快照进订单"},
	{"meta", "元数据：全局序号 seq、schema_version 等键值"},
	{"users", "买家账号表：手机号登录，PBKDF2 密码哈希 + 随机盐"},
	{"staff", "员工账号表：运营/仓库/客服，role 决定登录后落地页"},
	{"tx_guard", "全局写事务串行化行锁：写事务先 SELECT ... FOR UPDATE 此行"},
}

// ensureTableComments 幂等补表注释：只在库里的注释与期望不一致时执行 ALTER（metadata-only，很快）。
func (s *MySQLStore) ensureTableComments() error {
	for _, tc := range tableComments {
		var cur string
		err := s.db.QueryRow(`SELECT table_comment FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_name=?`, tc.table).Scan(&cur)
		if err != nil {
			continue // 表还不存在（异常情况）：留给建表流程
		}
		if cur == tc.comment {
			continue
		}
		// DDL 不支持占位符参数；注释是代码内常量，只需转义单引号
		ddl := "ALTER TABLE `" + tc.table + "` COMMENT='" + strings.ReplaceAll(tc.comment, "'", "''") + "'"
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("写入 %s 表注释失败：%w", tc.table, err)
		}
	}
	return nil
}

// ensureColumn 幂等补列：MySQL 8 不支持 ADD COLUMN IF NOT EXISTS，先查 information_schema。
func (s *MySQLStore) ensureColumn(table, column, ddl string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&n); err != nil {
		return fmt.Errorf("检查 %s.%s 失败：%w", table, column, err)
	}
	if n == 0 {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("为 %s 增加列 %s 失败：%w", table, column, err)
		}
	}
	return nil
}

func (s *MySQLStore) ensureProductCategoryColumn() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='products' AND column_name='category'`).Scan(&n); err != nil {
		return fmt.Errorf("检查 products.category 失败：%w", err)
	}
	if n == 0 {
		if _, err := s.db.Exec("ALTER TABLE products ADD COLUMN category VARCHAR(32) NOT NULL DEFAULT '其他'"); err != nil {
			return fmt.Errorf("为 products 增加 category 列失败：%w", err)
		}
	}
	var idx int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema=DATABASE() AND table_name='products' AND index_name='idx_products_category'`).Scan(&idx); err != nil {
		return fmt.Errorf("检查商品分类索引失败：%w", err)
	}
	if idx == 0 {
		if _, err := s.db.Exec("CREATE INDEX idx_products_category ON products (category)"); err != nil {
			return fmt.Errorf("创建商品分类索引失败：%w", err)
		}
	}
	return nil
}

// raiseSeq 把 meta.seq 抬到不小于「最大商品编号后缀」，防止 NextID 生成重复编号。
func (s *MySQLStore) raiseSeq() error {
	rows, err := s.db.Query("SELECT id FROM products WHERE id LIKE 'P%'")
	if err != nil {
		return fmt.Errorf("读取商品编号失败：%w", err)
	}
	maxID := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if n, err := strconv.Atoi(id[1:]); err == nil && n > maxID {
			maxID = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if maxID == 0 {
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO meta (k,v) VALUES ('seq',?)
		ON DUPLICATE KEY UPDATE v=GREATEST(CAST(v AS UNSIGNED), CAST(VALUES(v) AS UNSIGNED))`,
		strconv.Itoa(maxID))
	if err != nil {
		return fmt.Errorf("更新全局序号失败：%w", err)
	}
	return nil
}
