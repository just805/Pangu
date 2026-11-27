package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// ═══ M-12：开放 API 凭证 ═══
//
// 定位：**同一管理员账号下的程序化凭证**，不是多用户（平台保持单管理员形态是既定
// 决策）。Key 走与 JWT 相同的鉴权下游，因此创建的任务同样受拦截/审批/范围守卫约束。
//
// 安全口径：
//   - 明文只在创建时返回一次，库里只存 sha256 哈希；
//   - key_hint 保留末 4 位，列表里能认出是哪把；
//   - 高熵随机串直接哈希即可，不需要 bcrypt（不是人选的弱密码）；
//   - 权限范围（scope）三档：read < write < admin。程序化凭证一旦外泄，影响面应当
//     是「它能看/能改什么」，而不是整台平台——CI 只需要 write，没必要能装工具。

// API Key 权限范围。数值顺序即包含关系：admin 覆盖 write 覆盖 read。
const (
	APIKeyScopeRead  = "read"
	APIKeyScopeWrite = "write"
	APIKeyScopeAdmin = "admin"
)

// apiKeyScopeRank 把 scope 折成可比较的等级；未知值按最低权限处理（fail-closed）。
func apiKeyScopeRank(s string) int {
	switch s {
	case APIKeyScopeAdmin:
		return 3
	case APIKeyScopeWrite:
		return 2
	case APIKeyScopeRead:
		return 1
	default:
		return 0
	}
}

// NormalizeAPIKeyScope 规整外部输入。空值/未知值一律降到 read——新建 Key 时
// HTTP 层会先拒掉空值，这里是第二道兜底，保证库里不会出现越权档位。
func NormalizeAPIKeyScope(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case APIKeyScopeAdmin:
		return APIKeyScopeAdmin
	case APIKeyScopeWrite:
		return APIKeyScopeWrite
	default:
		return APIKeyScopeRead
	}
}

// APIKeyScopeAllows 判断持有 scope 的 Key 是否满足 required。
// 未知的 required 按 admin 处理（宁可多拦，不可漏放）。
func APIKeyScopeAllows(scope, required string) bool {
	if apiKeyScopeRank(required) == 0 {
		required = APIKeyScopeAdmin
	}
	return apiKeyScopeRank(scope) >= apiKeyScopeRank(required)
}

// APIKeyAuth 是一次成功校验的结果：调用方需要它做授权判定与审计。
type APIKeyAuth struct {
	ID    int64
	Scope string
}

// APIKey 是一把调用凭证的元信息（不含明文）。
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	KeyHint    string     `json:"key_hint"`
	Scope      string     `json:"scope"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

const apiKeyPrefix = "pk_"

// CreateAPIKey 生成一把新 Key，返回元信息与**仅此一次**的明文。
func (d *DB) CreateAPIKey(ctx context.Context, name, scope string, expiresAt *time.Time) (*APIKey, string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	raw := apiKeyPrefix + hex.EncodeToString(buf)
	hash := hashAPIKey(raw)
	hint := ""
	if len(raw) >= 4 {
		hint = "…" + raw[len(raw)-4:]
	}
	scope = NormalizeAPIKeyScope(scope)
	k := &APIKey{Name: strings.TrimSpace(name), KeyHint: hint, Scope: scope, Enabled: true}
	err := d.QueryRowContext(ctx, `
INSERT INTO api_keys(name, key_hash, key_hint, scope, expires_at) VALUES ($1,$2,$3,$4,$5)
RETURNING id, created_at`, k.Name, hash, hint, scope, expiresAt).Scan(&k.ID, &k.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return k, raw, nil
}

func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ListAPIKeys 返回全部 Key 的元信息（最新在前）。
func (d *DB) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := d.QueryContext(ctx, `
SELECT id, name, key_hint, scope, enabled, created_at, last_used_at, last_used_ip, expires_at
FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHint, &k.Scope, &k.Enabled, &k.CreatedAt, &k.LastUsedAt, &k.LastUsedIP, &k.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey 删除一把 Key（立即失效）。
func (d *DB) DeleteAPIKey(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM api_keys WHERE id=$1`, id)
	return err
}

// VerifyAPIKey 校验明文 Key：存在、启用、未过期。命中时更新 last_used_at / last_used_ip
// 并返回它的 scope 供调用方做授权判定。
// 任何查询失败都按「不通过」处理（鉴权失败必须 fail-closed）。
func (d *DB) VerifyAPIKey(ctx context.Context, raw, ip string) (APIKeyAuth, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, apiKeyPrefix) {
		return APIKeyAuth{}, false
	}
	var out APIKeyAuth
	err := d.QueryRowContext(ctx, `
UPDATE api_keys SET last_used_at=now(), last_used_ip=$2
WHERE key_hash=$1 AND enabled AND (expires_at IS NULL OR expires_at > now())
RETURNING id, scope`, hashAPIKey(raw), strings.TrimSpace(ip)).Scan(&out.ID, &out.Scope)
	if err != nil {
		return APIKeyAuth{}, false
	}
	if out.Scope == "" {
		// 存量库里 scope 为空的极端情况：按最小权限，不按管理员。
		out.Scope = APIKeyScopeRead
	}
	return out, out.ID > 0
}
