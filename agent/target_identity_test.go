package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PR #37 回归断言：会到达目标侧的产物——PoC 模板、落盘脚本、内置提示语、
// skills 脚本——不得携带平台/能力标识。这些字符串会随请求或写入落到被渗透方
// （SSRF 的参数值、上传请求的 multipart boundary、写进目标磁盘的文件正文、
// agent 工作目录里的脚本文件名），客户蓝队会把它们当成攻击者的签名，进而产生
// 指向本产品的假 IoC、误导应急响应。
//
// 注意范围：平台侧字段（任务 ID、拦截提示、/opt/pangu 数据路径）不在此列——
// 它们不出站。这里只盯「会出现在目标侧日志/磁盘/请求里」的内容。

// targetIdentityBanned 是平台名与历史旧名的全部形态。
var targetIdentityBanned = []string{"pangu", "PANGU", "pojun", "POJUN", "盘古", "破军"}

// targetIdentityCheck 断言 s 不含任何平台标识。
func targetIdentityCheck(t *testing.T, what, s string) {
	t.Helper()
	low := strings.ToLower(s)
	for _, b := range targetIdentityBanned {
		if strings.Contains(low, strings.ToLower(b)) {
			t.Errorf("%s 含平台标识 %q——它会落到被渗透方侧", what, b)
		}
	}
}

// TestTargetIdentityPocTemplates：PoC 脚手架的公共段与各类别模板。
func TestTargetIdentityPocTemplates(t *testing.T) {
	targetIdentityCheck(t, "poc_scaffold 公共段", pocCommon)
	for cat, tpl := range pocTemplates {
		targetIdentityCheck(t, "poc_scaffold/"+cat, tpl)
	}
}

// TestTargetIdentityDropScripts：落盘脚本（探活/前端审计）正文与文件名。
func TestTargetIdentityDropScripts(t *testing.T) {
	targetIdentityCheck(t, "http_probe 脚本", httpProbeScript)
	targetIdentityCheck(t, "js_audit 脚本", jsAuditScript)
	// 脚本文件名也要不带产品名：文件名会出现在 agent 工作目录与目标侧磁盘。
	for _, name := range []string{filepath.Join("x", "probe.sh"), filepath.Join("x", "js_audit.py")} {
		targetIdentityCheck(t, "落盘脚本名 "+name, name)
	}
}

// TestTargetIdentitySystemPrompts：内置提示语模板与代码注入尾巴。worker 会把
// 这些话术复现进它写的脚本/请求（例如测试 marker 命名），平台名跟着落盘。
func TestTargetIdentitySystemPrompts(t *testing.T) {
	for name, s := range map[string]string{
		"worker 模板":           workerDefaultTmpl,
		"planner 模板":          plannerDefaultTmpl,
		"mainagent 模板":        mainAgentDefaultTmpl,
		"scannerFirst":        scannerFirstGuidance,
		"skillRouting":        skillRoutingGuidance,
		"coverageMatrix":      coverageMatrixPlannerGuidance,
		"scopeVerify":         scopeVerifyPlannerGuidance,
		"execFaceWorker":      execFaceWorkerGuidance,
		"execFacePlanner":     execFacePlannerGuidance,
		"egressProxyGuidance": egressProxyGuidance("127.0.0.1:8788"),
	} {
		targetIdentityCheck(t, "提示语 "+name, s)
	}
}

// TestTargetIdentityMarkerPrefix：写进目标侧测试对象的 marker 前缀必须是中性的
// qa-test-*（仍可用于枚举清理），不得是平台名。
func TestTargetIdentityMarkerPrefix(t *testing.T) {
	// marker 只出现在 worker 侧验证提示语（写测试对象的人）；编排版不涉及。
	if !strings.Contains(impactVerificationGuidance, "qa-test-") {
		t.Errorf("验证提示语缺少 qa-test- marker 规范（写测试对象需要统一中性前缀）")
	}
	targetIdentityCheck(t, "验证提示语 marker", impactVerificationGuidance)
}

// TestTargetIdentitySkillsScripts：skills 目录内的可执行脚本（会整份落盘/执行）
// 不含平台标识。SKILL.md 属于平台侧指导文档，不在断言范围。
func TestTargetIdentitySkillsScripts(t *testing.T) {
	skillsDir := findSkillsDir(t)
	if skillsDir == "" {
		t.Skip("skills 目录不可定位（非源码布局运行）")
	}
	re := regexp.MustCompile(`\.(py|sh|js)$`)
	err := filepath.Walk(skillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !re.MatchString(path) {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		targetIdentityCheck(t, "skills 脚本 "+filepath.Base(path), string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walk skills: %v", err)
	}
}

// findSkillsDir 从测试工作目录向上找 skills/（源码布局内运行时即仓库根）。
func findSkillsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		return ""
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "skills")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// platformDefaultUA 是 http_request / poc_scaffold / js_audit / enrich 共用的
// 出站 UA——四个出口必须逐字节一致，任何一个跑偏都是全网唯一、可被目标侧跨请求
// 关联的指纹。
const platformDefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// TestTargetIdentityUADoesNotLeakPlatform：各处 UA 不带平台名。
func TestTargetIdentityUADoesNotLeakPlatform(t *testing.T) {
	targetIdentityCheck(t, "平台默认 UA", platformDefaultUA)
}
