package hostd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/just805/pangu/selfupdate"
)

// ValidVersionTag 报告 tag 是否是允许的版本号形态。校验实现放在 selfupdate，
// 与「按版本号取 Release」的逻辑待在一起，避免两处规则漂移。
func ValidVersionTag(tag string) bool { return selfupdate.ValidVersionTag(tag) }

// downgradeAllowed 是降级禁令的显式逃生口（回滚演练用）。
func downgradeAllowed() bool {
	v := strings.TrimSpace(os.Getenv("PANGU_ALLOW_DOWNGRADE"))
	return v == "1" || strings.EqualFold(v, "true")
}

// UpgradeSelf 把助手自己换装到指定 Release 版本。
//
// 为什么必须由助手自己做：它装在 root 拥有的 /usr/local/bin，而主服务受
// ProtectSystem=strict 限制写不进去。
//
// 为什么是「自己下载」而不是「接收主服务递来的文件」：主服务只以运行业务用户的
// 权限跑，一旦它被攻破，让助手安装它指定的任意二进制就等于把 root 执行权交出去。
// 这里坚持自己从官方 Release 拉包、自己按 SHA256SUMS 校验、自己冒烟测试，
// 主服务能影响的只有「装哪个已发布版本」，影响不到「装什么内容」。
func (s *Server) UpgradeSelf(ctx context.Context, tag, proxy string) error {
	tag = strings.TrimSpace(tag)
	if !ValidVersionTag(tag) {
		return fmt.Errorf("版本号格式非法: %q", tag)
	}
	// 拒绝降级：self-upgrade 的语义是「主服务升级后的版本对齐」，把 root 拥有的
	// 助手换回旧版本没有任何正当场景，反而给能连上 socket 的属组成员一条把已知
	// 漏洞装回去的路。版本不可比较（dev/本地构建）或显式 PANGU_ALLOW_DOWNGRADE=1
	// 时放行——前者本就没有「当前版本」基线可判。
	if !downgradeAllowed() && s.Version != "" {
		if cmp, ok := selfupdate.CompareVersions(strings.TrimPrefix(tag, "v"), strings.TrimPrefix(s.Version, "v")); ok && cmp < 0 {
			return fmt.Errorf("拒绝降级：%s 低于当前版本 %s（如确需回滚，设置 PANGU_ALLOW_DOWNGRADE=1）", tag, s.Version)
		}
	}
	if s.SelfPath == "" {
		return fmt.Errorf("未知自身路径，无法换装")
	}
	dir := filepath.Dir(s.SelfPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("准备目标目录 %s: %w", dir, err)
	}

	c := selfupdate.NewClient(proxy)
	rel, err := selfupdate.FetchByTag(ctx, c, tag)
	if err != nil {
		return err
	}
	// zip 与临时二进制都落在目标目录：跨文件系统 rename 会失败，
	// 而 /tmp 常常是独立挂载点。
	zipPath, err := selfupdate.FetchVerifiedZip(ctx, c, rel, dir, nil)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(zipPath) }()

	tmp := filepath.Join(dir, ".pangu-hostd.new")
	defer func() { _ = os.Remove(tmp) }()
	if err := selfupdate.ExtractMember(zipPath, "pangu-hostd", tmp); err != nil {
		return fmt.Errorf("发布包里没有 pangu-hostd：%w", err)
	}
	if err := selfupdate.SmokeTestBinary(tmp, "-version"); err != nil {
		return fmt.Errorf("新版本无法在当前系统运行：%w", err)
	}
	if err := selfupdate.ReplaceBinary(tmp, s.SelfPath); err != nil {
		return err
	}
	return nil
}
