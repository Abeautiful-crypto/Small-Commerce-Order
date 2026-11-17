// Package account 提供登录账号仓库：用户表（C 端买家）与管理人员表（B 端工作台）。
//
// 设计要点：
//   - 登录账号是「11 位手机号」，角色由服务端账号记录决定，登录请求不能自报角色；
//   - 密码以 PBKDF2-HMAC-SHA256 加盐派生后存储，库里只有密文与盐，不可逆；
//   - MySQL 与内存两种实现语义一致，内存实现供离线演示与单元测试使用。
package account

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"small-commerce-order/internal/model"
)

// Account 是登录成功后返回的身份信息（不含任何密码字段）。
// ID 是内部主键（订单归属、权限守卫都用它），Phone 是登录账号。
type Account struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Role  model.Role `json:"role"`
	Phone string     `json:"phone,omitempty"`
}

// Repo 是登录账号仓库。
type Repo interface {
	// Find 按内部 ID 查询身份（会话还原、权限守卫使用）。
	Find(id string) (Account, bool)
	// FindByPhone 按登录手机号查询身份。
	FindByPhone(phone string) (Account, bool)
	// Authenticate 按手机号 + 密码校验；账号不存在或密码错误均返回 false。
	Authenticate(phone, password string) (Account, bool)
	// Register 注册一个买家账号（手机号唯一）。
	Register(name, phone, password string) (Account, error)
	// CreateStaff 由管理员新增一个 B 端员工账号（运营 / 仓库 / 客服 / 管理员）。
	CreateStaff(name, phone, password string, role model.Role) (Account, error)
	// ListStaff 返回全部 B 端员工账号（不含密码），供管理员页展示。
	ListStaff() []Account
	// List 返回全部账号，供身份目录同步与运维排查（不下发前端）。
	List() []Account
}

// RegisterError 注册失败的业务原因，供 API 层映射 HTTP 状态码。
type RegisterError string

func (e RegisterError) Error() string { return string(e) }

const (
	ErrPhoneTaken      RegisterError = "该手机号已注册"
	ErrInvalidPhone    RegisterError = "手机号必须是 11 位数字"
	ErrWeakPassword    RegisterError = "密码至少 6 位"
	ErrTooLongPassword RegisterError = "密码最多 32 位"
	ErrStaffRole       RegisterError = "员工角色只能是运营、仓库、客服或管理员"
	ErrEmptyName       RegisterError = "姓名不能为空"
)

// IsStaffRole 判断角色是否属于 B 端员工角色（管理员新增账号时使用）。
func IsStaffRole(role model.Role) bool {
	switch role {
	case model.RoleOps, model.RoleWarehouse, model.RoleCS, model.RoleAdmin:
		return true
	}
	return false
}

// validateStaff 校验新增员工入参。
func validateStaff(name, phone, password string, role model.Role) error {
	if strings.TrimSpace(name) == "" {
		return ErrEmptyName
	}
	if !IsStaffRole(role) {
		return ErrStaffRole
	}
	return ValidateRegistration(phone, password)
}

// ValidateRegistration 校验注册入参：手机号 11 位数字、密码 6-32 位。
func ValidateRegistration(phone, password string) error {
	if !IsPhone(phone) {
		return ErrInvalidPhone
	}
	if len(password) < 6 {
		return ErrWeakPassword
	}
	if len(password) > 32 {
		return ErrTooLongPassword
	}
	return nil
}

// IsPhone 判断是否是可作为登录账号的 11 位手机号。
func IsPhone(s string) bool {
	if len(s) != 11 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// seedAccount 是首次建库写入的演示账号；建库后以数据库中的记录为准。
type seedAccount struct {
	ID       string
	Name     string
	Role     model.Role
	Phone    string
	Password string
}

// Seed 预置账号：3 名买家、运营 / 仓库 / 客服各 1 名，登录账号统一为 11 位手机号。
// 初始密码统一为 123456，可在用户表 / 管理人员表直接改 password_hash 与 salt 更换。
var Seed = []seedAccount{
	{ID: "u001", Name: "张小买", Role: model.RoleBuyer, Phone: "13800000001", Password: "123456"},
	{ID: "u002", Name: "李小购", Role: model.RoleBuyer, Phone: "13800000002", Password: "123456"},
	{ID: "u003", Name: "王小单", Role: model.RoleBuyer, Phone: "13800000003", Password: "123456"},
	{ID: "admin", Name: "运营专员", Role: model.RoleOps, Phone: "13900000001", Password: "123456"},
	{ID: "wh", Name: "仓库管理员", Role: model.RoleWarehouse, Phone: "13900000002", Password: "123456"},
	{ID: "cs", Name: "客服小花", Role: model.RoleCS, Phone: "13900000003", Password: "123456"},
	{ID: "su", Name: "系统管理员", Role: model.RoleAdmin, Phone: "13900000000", Password: "123456"},
}

// ---------------- 密码派生 ----------------

const (
	iterations = 100000
	saltBytes  = 16
	keyBytes   = 32
)

// HashPassword 生成「盐 + 派生密文」，两者都以十六进制存储。
func HashPassword(password string) (hash, salt string, err error) {
	raw := make([]byte, saltBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("生成密码盐失败：%w", err)
	}
	dk := pbkdf2SHA256([]byte(password), raw, iterations, keyBytes)
	return hex.EncodeToString(dk), hex.EncodeToString(raw), nil
}

// VerifyPassword 常量时间校验明文与「盐 + 派生密文」是否匹配。
func VerifyPassword(password, hash, salt string) bool {
	rawSalt, err := hex.DecodeString(salt)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(hash)
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(password), rawSalt, iterations, keyBytes)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 是 RFC 8018 PBKDF2 的 HMAC-SHA256 实现（标准库即可，无需外部依赖）。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hLen := sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	var idx [4]byte
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(idx[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(idx[:])
		u := mac.Sum(nil)
		t := make([]byte, hLen)
		copy(t, u)
		for n := 2; n <= iter; n++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(nil)
			for i := 0; i < hLen; i++ {
				t[i] ^= u[i]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// ---------------- MySQL 实现 ----------------

// MySQLRepo 以 users（买家）/ staff（管理人员）两张表作为账号真相源。
type MySQLRepo struct {
	db *sql.DB
}

func NewMySQLRepo(db *sql.DB) *MySQLRepo { return &MySQLRepo{db: db} }

// EnsureSchema 幂等升级账号表：补 phone 列与唯一索引，回填种子账号手机号，并写入缺失的种子。
// 老库（只有 id/name/password_hash/salt）升级后即可用手机号登录，历史订单归属不受影响。
func (r *MySQLRepo) EnsureSchema(now time.Time) error {
	tables := []struct{ table, index string }{
		{"users", "uk_users_phone"},
		{"staff", "uk_staff_phone"},
	}
	for _, t := range tables {
		if err := r.ensurePhoneColumn(t.table); err != nil {
			return err
		}
		if err := r.ensureUniqueIndex(t.table, t.index, "phone"); err != nil {
			return err
		}
	}
	for _, s := range Seed {
		hash, salt, err := HashPassword(s.Password)
		if err != nil {
			return err
		}
		if s.Role == model.RoleBuyer {
			// 已存在的行只补手机号，不动密码（保留运行中改过的密码）
			if _, err := r.db.Exec(`INSERT INTO users (id,name,phone,password_hash,salt,created_at)
				VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE phone=IFNULL(phone, VALUES(phone))`,
				s.ID, s.Name, s.Phone, hash, salt, now); err != nil {
				return fmt.Errorf("写入用户表失败：%w", err)
			}
			continue
		}
		if _, err := r.db.Exec(`INSERT INTO staff (id,name,role,phone,password_hash,salt,created_at)
			VALUES (?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE phone=IFNULL(phone, VALUES(phone))`,
			s.ID, s.Name, string(s.Role), s.Phone, hash, salt, now); err != nil {
			return fmt.Errorf("写入管理人员表失败：%w", err)
		}
	}
	return nil
}

func (r *MySQLRepo) ensurePhoneColumn(table string) error {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name=? AND column_name='phone'`, table).Scan(&n)
	if err != nil {
		return fmt.Errorf("检查 %s.phone 失败：%w", table, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := r.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN phone VARCHAR(32) NULL DEFAULT NULL", table)); err != nil {
		return fmt.Errorf("为 %s 增加 phone 列失败：%w", table, err)
	}
	return nil
}

func (r *MySQLRepo) ensureUniqueIndex(table, index, column string) error {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema=DATABASE() AND table_name=? AND index_name=?`, table, index).Scan(&n)
	if err != nil {
		return fmt.Errorf("检查索引 %s 失败：%w", index, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := r.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD UNIQUE KEY %s (%s)", table, index, column)); err != nil {
		return fmt.Errorf("创建索引 %s 失败：%w", index, err)
	}
	return nil
}

// Find 按内部 ID 查询（先管理人员表，再用户表，两侧主键互不重叠）。
func (r *MySQLRepo) Find(id string) (Account, bool) {
	a, _, _, ok := r.lookup(`id`, id)
	return a, ok
}

// FindByPhone 按登录手机号查询。
func (r *MySQLRepo) FindByPhone(phone string) (Account, bool) {
	a, _, _, ok := r.lookup(`phone`, phone)
	return a, ok
}

func (r *MySQLRepo) lookup(column, value string) (Account, string, string, bool) {
	var (
		a     Account
		role  string
		hash  string
		salt  string
		phone sql.NullString
	)
	q := func(field string) string {
		return fmt.Sprintf(`SELECT id,name,role,phone,password_hash,salt FROM staff WHERE %s=?`, field)
	}
	err := r.db.QueryRow(q(column), value).Scan(&a.ID, &a.Name, &role, &phone, &hash, &salt)
	if err == nil {
		a.Role = model.Role(role)
		a.Phone = phone.String
		return a, hash, salt, true
	}
	if err != sql.ErrNoRows {
		return Account{}, "", "", false
	}
	// users 表没有 role 列，统一按买家返回
	err = r.db.QueryRow(fmt.Sprintf(`SELECT id,name,phone,password_hash,salt FROM users WHERE %s=?`, column), value).
		Scan(&a.ID, &a.Name, &phone, &hash, &salt)
	if err != nil {
		return Account{}, "", "", false
	}
	a.Role = model.RoleBuyer
	a.Phone = phone.String
	return a, hash, salt, true
}

func (r *MySQLRepo) Authenticate(phone, password string) (Account, bool) {
	a, hash, salt, ok := r.lookup(`phone`, phone)
	if !ok {
		return Account{}, false
	}
	if !VerifyPassword(password, hash, salt) {
		return Account{}, false
	}
	return a, true
}

// Register 注册买家账号：手机号唯一，密码以 PBKDF2 密文存储。
// 内部 ID 由手机号派生（u+手机号），与预置账号 u001 等互不冲突。
func (r *MySQLRepo) Register(name, phone, password string) (Account, error) {
	if err := ValidateRegistration(phone, password); err != nil {
		return Account{}, err
	}
	if _, ok := r.FindByPhone(phone); ok {
		return Account{}, ErrPhoneTaken
	}
	hash, salt, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	a := Account{ID: "u" + phone, Name: name, Role: model.RoleBuyer, Phone: phone}
	if _, err := r.db.Exec(`INSERT INTO users (id,name,phone,password_hash,salt,created_at) VALUES (?,?,?,?,?,?)`,
		a.ID, a.Name, a.Phone, hash, salt, time.Now()); err != nil {
		return Account{}, fmt.Errorf("写入用户表失败：%w", err)
	}
	return a, nil
}

// CreateStaff 由管理员新增 B 端员工账号：手机号唯一，ID 由手机号派生（s+手机号）。
func (r *MySQLRepo) CreateStaff(name, phone, password string, role model.Role) (Account, error) {
	name = strings.TrimSpace(name)
	if err := validateStaff(name, phone, password, role); err != nil {
		return Account{}, err
	}
	if _, ok := r.FindByPhone(phone); ok {
		return Account{}, ErrPhoneTaken
	}
	hash, salt, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	a := Account{ID: "s" + phone, Name: name, Role: role, Phone: phone}
	if _, err := r.db.Exec(`INSERT INTO staff (id,name,role,phone,password_hash,salt,created_at) VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.Name, string(a.Role), a.Phone, hash, salt, time.Now()); err != nil {
		return Account{}, fmt.Errorf("写入管理人员表失败：%w", err)
	}
	return a, nil
}

// ListStaff 只返回 staff 表（B 端），不含密码。
func (r *MySQLRepo) ListStaff() []Account {
	out := []Account{}
	rows, err := r.db.Query(`SELECT id,name,role,phone FROM staff ORDER BY created_at, id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		var role string
		var phone sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &role, &phone); err != nil {
			break
		}
		a.Role = model.Role(role)
		a.Phone = phone.String
		out = append(out, a)
	}
	return out
}

func (r *MySQLRepo) List() []Account {
	out := []Account{}
	rows, err := r.db.Query(`SELECT id,name,role,phone FROM staff ORDER BY id`)
	if err == nil {
		for rows.Next() {
			var a Account
			var role string
			var phone sql.NullString
			if err := rows.Scan(&a.ID, &a.Name, &role, &phone); err != nil {
				break
			}
			a.Role = model.Role(role)
			a.Phone = phone.String
			out = append(out, a)
		}
		rows.Close()
	}
	rows, err = r.db.Query(`SELECT id,name,phone FROM users ORDER BY id`)
	if err == nil {
		for rows.Next() {
			var a Account
			var phone sql.NullString
			if err := rows.Scan(&a.ID, &a.Name, &phone); err != nil {
				break
			}
			a.Role = model.RoleBuyer
			a.Phone = phone.String
			out = append(out, a)
		}
		rows.Close()
	}
	return out
}

// ---------------- 内存实现 ----------------

type credential struct {
	acc  Account
	hash string
	salt string
}

// MemRepo 是离线模式下的账号仓库，内容与 Seed 一致。
type MemRepo struct {
	mu      sync.RWMutex
	byID    map[string]credential
	byPhone map[string]credential
}

func NewMemRepo() *MemRepo {
	r := &MemRepo{byID: map[string]credential{}, byPhone: map[string]credential{}}
	for _, s := range Seed {
		hash, salt, err := HashPassword(s.Password)
		if err != nil {
			continue
		}
		c := credential{acc: Account{ID: s.ID, Name: s.Name, Role: s.Role, Phone: s.Phone}, hash: hash, salt: salt}
		r.byID[s.ID] = c
		r.byPhone[s.Phone] = c
	}
	return r
}

func (r *MemRepo) Find(id string) (Account, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return Account{}, false
	}
	return c.acc, true
}

func (r *MemRepo) FindByPhone(phone string) (Account, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byPhone[phone]
	if !ok {
		return Account{}, false
	}
	return c.acc, true
}

func (r *MemRepo) Authenticate(phone, password string) (Account, bool) {
	r.mu.RLock()
	c, ok := r.byPhone[phone]
	r.mu.RUnlock()
	if !ok || !VerifyPassword(password, c.hash, c.salt) {
		return Account{}, false
	}
	return c.acc, true
}

// Register 离线模式注册：与 MySQL 实现保持同样的校验与 ID 规则。
func (r *MemRepo) Register(name, phone, password string) (Account, error) {
	if err := ValidateRegistration(phone, password); err != nil {
		return Account{}, err
	}
	hash, salt, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byPhone[phone]; ok {
		return Account{}, ErrPhoneTaken
	}
	a := Account{ID: "u" + phone, Name: name, Role: model.RoleBuyer, Phone: phone}
	c := credential{acc: a, hash: hash, salt: salt}
	r.byID[a.ID] = c
	r.byPhone[phone] = c
	return a, nil
}

// CreateStaff 离线模式新增 B 端员工：规则与 MySQL 实现一致。
func (r *MemRepo) CreateStaff(name, phone, password string, role model.Role) (Account, error) {
	name = strings.TrimSpace(name)
	if err := validateStaff(name, phone, password, role); err != nil {
		return Account{}, err
	}
	hash, salt, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byPhone[phone]; ok {
		return Account{}, ErrPhoneTaken
	}
	a := Account{ID: "s" + phone, Name: name, Role: role, Phone: phone}
	c := credential{acc: a, hash: hash, salt: salt}
	r.byID[a.ID] = c
	r.byPhone[phone] = c
	return a, nil
}

// ListStaff 只返回 B 端角色账号。
func (r *MemRepo) ListStaff() []Account {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Account{}
	for _, c := range r.byID {
		if IsStaffRole(c.acc.Role) {
			out = append(out, c.acc)
		}
	}
	return out
}

func (r *MemRepo) List() []Account {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Account, 0, len(r.byID))
	for _, c := range r.byID {
		out = append(out, c.acc)
	}
	return out
}
