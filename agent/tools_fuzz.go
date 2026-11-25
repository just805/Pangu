package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

// ═══ M-01：模糊测试引擎（规划器）═══
//
// 定位：把漏洞挖掘方法论里「数据边界」「资源假设」两个维度，从「模型现场手写脚本」
// 变成平台可编排的动作。与 http_probe / api_contract 等工具同一模式——
// **只落脚本 + 组命令，不自己发请求**：流量照常过 MITM 留痕、审批照常生效，
// 后台长跑交给既有的 job_* 作业，不新增后台协程。
//
// 三种入口：
//   api      —— 契约驱动（schemathesis）：按 OpenAPI/Swagger 生成并校验参数组合；
//   protocol —— 协议模糊（boofuzz）：生成可运行的骨架脚本，按真实报文改结构；
//   param    —— 参数变异（ffuf）：在 URL 的 FUZZ 占位处按字典换值。
//
// 工具输出的是**命令**，不是结论。异常样本要落 save_evidence、确认的漏洞走
// report_finding 并绑定流量——这样模糊测试的产物与其它证据同一口径。
const (
	fuzzDefaultRequests = 500
	fuzzMaxRequests     = 5000
	fuzzDefaultRate     = 5
	fuzzMaxRate         = 50
	fuzzDefaultDuration = 300
	fuzzMaxDuration     = 3600
)

// fuzzPlanInput 是 fuzz_plan 的入参。
type fuzzPlanInput struct {
	Mode      string   `json:"mode"`       // api | protocol | param
	Target    string   `json:"target"`     // api/param: 目标 URL；protocol: host:port
	Contract  string   `json:"contract"`   // api 模式：契约 URL 或本地文件
	TrafficID string   `json:"traffic_id"` // param 模式可选：作为种子参考的已录流量 ID
	Requests  int      `json:"requests"`   // 最大请求数
	Rate      int      `json:"rate"`       // 每秒请求上限
	Duration  int      `json:"duration"`   // 时长上限（秒），用 GNU timeout 包装
	Headers   []string `json:"headers"`    // 额外请求头，形如 "Name: value"
	Wordlist  string   `json:"wordlist"`   // param 模式：字典文件路径
}

func (t *ToolSet) fuzzPlan() actool.CoreTool {
	return readTool(
		"fuzz_plan",
		"生成模糊测试的可执行命令（不自己发请求，交给 Bash 执行，流量照常录制、审批照常生效）。"+
			"三种模式：\n"+
			"  · api —— 契约驱动（schemathesis）：按 OpenAPI/Swagger 自动生成参数组合并校验响应；\n"+
			"  · protocol —— 协议模糊（boofuzz）：落一个骨架脚本并给出运行命令，需按真实报文改结构；\n"+
			"  · param —— 参数变异（ffuf）：在 URL 的 FUZZ 占位处按字典换值。\n"+
			"适用场景：手工构造边界值/畸形输入收益低、需要大批量系统化覆盖的参数位与协议字段。"+
			"长跑请用 job_start 包住返回的命令；异常样本用 save_evidence 存档，"+
			"确认的漏洞用 report_finding 上报并绑定流量证据。",
		obj(map[string]any{
			"mode": str("api | protocol | param"),
			"target": str("api/param 模式填目标 URL（param 模式必须含 FUZZ 占位，如 https://h/api?id=FUZZ）；" +
				"protocol 模式填 host:port"),
			"contract":   str("api 模式必填：OpenAPI/Swagger 契约的 URL 或本地文件路径（可来自 api_contract 的输出）"),
			"traffic_id": str("param 模式可选：作为种子参考的已录流量 ID（traffic_search 返回的真实 ID）"),
			"requests":   intp(fmt.Sprintf("最大请求数，默认 %d，上限 %d", fuzzDefaultRequests, fuzzMaxRequests)),
			"rate":       intp(fmt.Sprintf("每秒请求上限，默认 %d，上限 %d（避免把目标打挂）", fuzzDefaultRate, fuzzMaxRate)),
			"duration":   intp(fmt.Sprintf("时长上限秒，默认 %d，上限 %d（GNU timeout 包装）", fuzzDefaultDuration, fuzzMaxDuration)),
			"headers":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "额外请求头，形如 \"Name: value\"（可选）"},
			"wordlist":   str("param 模式必填：字典文件路径（宿主环境里的 SecLists 等）"),
		}, "mode", "target"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a fuzzPlanInput
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			// 落 protocol 骨架脚本需要工作目录；其它模式不依赖它。
			scriptPath := ""
			if strings.ToLower(strings.TrimSpace(a.Mode)) == "protocol" {
				scriptPath = t.ensureProtocolFuzzScript(a.Target)
			}
			cmd, warnings, err := fuzzCommand(a, scriptPath)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := map[string]any{
				"mode":          strings.ToLower(strings.TrimSpace(a.Mode)),
				"run_with_bash": cmd,
				"note": "命令只做生成，未发送任何请求。执行后：异常报文/响应落 save_evidence，" +
					"确认的漏洞用 report_finding 上报并绑定 traffic_refs（流量已由记录代理留存）。",
			}
			if len(warnings) > 0 {
				out["warnings"] = warnings
			}
			return jsonResult(out)
		})
}

// validateFuzzHeaders 校验并归一请求头。与平台既有约定一致：拒绝 CRLF 注入与
// curl 的 @ 前缀（后者会读取本地文件）。返回规范化后的头与错误。
func validateFuzzHeaders(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, h := range raw {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, "\r\n") {
			return nil, fmt.Errorf("请求头不允许包含换行符（防止 CRLF 注入）：%q", h)
		}
		if strings.HasPrefix(h, "@") {
			return nil, fmt.Errorf("@ 开头的请求头会被当成文件读取，已拒绝：%q", h)
		}
		name, value, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("请求头格式应为 \"Name: value\"：%q", h)
		}
		out = append(out, strings.TrimSpace(name)+": "+strings.TrimSpace(value))
	}
	return out, nil
}

// clampFuzzInt 把请求数 / 速率 / 时长夹到安全区间。
func clampFuzzInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// fuzzCommand 生成待执行的 shell 命令与缺口提示。纯函数（scriptPath 由调用方落盘
// 后传入），便于单测。
func fuzzCommand(in fuzzPlanInput, scriptPath string) (cmd string, warnings []string, err error) {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	target := strings.TrimSpace(in.Target)
	requests := clampFuzzInt(in.Requests, fuzzDefaultRequests, fuzzMaxRequests)
	rate := clampFuzzInt(in.Rate, fuzzDefaultRate, fuzzMaxRate)
	duration := clampFuzzInt(in.Duration, fuzzDefaultDuration, fuzzMaxDuration)
	headers, err := validateFuzzHeaders(in.Headers)
	if err != nil {
		return "", nil, err
	}
	// GNU timeout 包装：模糊测试最怕失控长跑，统一由 timeout 兜底。
	prefix := "timeout " + strconv.Itoa(duration) + "s "

	switch mode {
	case "api":
		contract := strings.TrimSpace(in.Contract)
		if contract == "" {
			return "", nil, fmt.Errorf("api 模式需要 contract：OpenAPI/Swagger 契约的 URL 或本地文件路径（可用 api_contract 先探测）")
		}
		if target == "" || (!strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://")) {
			return "", nil, fmt.Errorf("api 模式需要 target：目标的 http(s) 绝对地址")
		}
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString("schemathesis run ")
		b.WriteString(shellSingleQuote(contract))
		b.WriteString(" --base-url ")
		b.WriteString(shellSingleQuote(target))
		b.WriteString(" --max-examples ")
		b.WriteString(strconv.Itoa(requests))
		b.WriteString(" --workers 1")
		for _, h := range headers {
			b.WriteString(" -H ")
			b.WriteString(shellSingleQuote(h))
		}
		warnings = append(warnings,
			"schemathesis 的参数随版本变化：若报未知参数（如 --max-examples），按 `schemathesis run --help` 调整后再执行。",
			"速率控制由 --workers 1 与 --max-examples 近似；需要严格限速时加 --rate-limit "+strconv.Itoa(rate)+"（旧版本不支持）。")
		return b.String(), warnings, nil

	case "param":
		if target == "" {
			return "", nil, fmt.Errorf("param 模式需要 target")
		}
		if !strings.Contains(target, "FUZZ") {
			return "", nil, fmt.Errorf("param 模式的 target 必须包含 FUZZ 占位符，例如 https://h/api?id=FUZZ——模糊引擎会在该位置按字典替换")
		}
		wordlist := strings.TrimSpace(in.Wordlist)
		if wordlist == "" {
			return "", nil, fmt.Errorf("param 模式需要 wordlist：字典文件路径（宿主环境可装 SecLists）")
		}
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString("ffuf -u ")
		b.WriteString(shellSingleQuote(target))
		b.WriteString(" -w ")
		b.WriteString(shellSingleQuote(wordlist))
		b.WriteString(" -rate ")
		b.WriteString(strconv.Itoa(rate))
		b.WriteString(" -t 1 -of json -o fuzz_param.json -s")
		for _, h := range headers {
			b.WriteString(" -H ")
			b.WriteString(shellSingleQuote(h))
		}
		if tid := strings.TrimSpace(in.TrafficID); tid != "" {
			warnings = append(warnings, "traffic_id "+tid+" 仅作种子参考：先用 traffic_get 看清真实参数结构，再确认 target 的 FUZZ 位置。")
		}
		warnings = append(warnings,
			"结果写 fuzz_param.json（当前工作目录）：按 status/words/length 异常筛出候选，再逐条复核。")
		return b.String(), warnings, nil

	case "protocol":
		if !strings.Contains(target, ":") {
			return "", nil, fmt.Errorf("protocol 模式的 target 应为 host:port，例如 10.0.0.5:6379")
		}
		if scriptPath == "" {
			return "", nil, fmt.Errorf("protocol 模式未启用：本次 run 没有可用的工作目录（无法落骨架脚本）")
		}
		warnings = append(warnings,
			"骨架脚本只是可运行的最小结构：先按协议真实报文改写 s_string/s_static 序列，否则变异是无意义的随机字节。",
			"boofuzz 未安装时先在「宿主环境」装 boofuzz；目标端崩溃/异常响应用 save_evidence 存档。")
		return prefix + "python3 " + shellSingleQuote(scriptPath), warnings, nil
	}
	return "", nil, fmt.Errorf("mode 只能是 api | protocol | param，收到 %q", in.Mode)
}

// protocolFuzzScriptTmpl 是 boofuzz 骨架脚本模板。占位符：__HOST__ / __PORT__。
const protocolFuzzScriptTmpl = `#!/usr/bin/env python3
# 由 pangu fuzz_plan 生成：协议模糊骨架（目标 __HOST__:__PORT__）。
# 注意：下面的报文结构是占位示例——请按目标的真实协议字段改写，
# 否则变异只会产生无意义的随机字节，拿不到有效结论。
from boofuzz import Session, TargetConnection, s_initialize, s_string, s_static, s_get

TARGET_HOST = '__HOST__'
TARGET_PORT = __PORT__


def main() -> None:
    session = Session(
        target=TargetConnection(host=TARGET_HOST, port=TARGET_PORT),
        sleep_time=0.05,
        restart_interval=50,
    )
    s_initialize('probe')
    s_string('A' * 16)      # ← 替换为协议真实字段（长度/类型/枚举边界）
    s_static(b'\r\n')
    session.connect(s_get('probe'))
    session.fuzz()


if __name__ == '__main__':
    main()
`

// ensureProtocolFuzzScript 把骨架脚本落到本次 run 的工作目录，返回路径。
// 与 ensureProbeScript 同一约定：每次调用覆盖写；失败返回空串（调用方报错）。
func (t *ToolSet) ensureProtocolFuzzScript(target string) string {
	if strings.TrimSpace(t.runDir) == "" {
		return ""
	}
	host, port, ok := splitHostPort(strings.TrimSpace(target))
	if !ok {
		return ""
	}
	script := strings.NewReplacer(
		"__HOST__", pySingleQuote(host),
		"__PORT__", strconv.Itoa(port),
	).Replace(protocolFuzzScriptTmpl)
	p := filepath.Join(t.runDir, "fuzz_protocol.py")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		return ""
	}
	return p
}

// splitHostPort 解析 host:port；端口非法时返回 ok=false。
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", 0, false
	}
	host := strings.TrimSpace(s[:i])
	port, err := strconv.Atoi(strings.TrimSpace(s[i+1:]))
	if err != nil || port <= 0 || port > 65535 || host == "" {
		return "", 0, false
	}
	return host, port, true
}

// pySingleQuote 把值安全嵌进 Python 单引号字符串。
func pySingleQuote(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "'", `\'`)
}
