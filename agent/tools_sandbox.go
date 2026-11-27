package agent

// ═══ M-16 P1：PoC 预验证工具 ═══
//
// 一个工具解决一件事：把「这个 PoC 到底对不对」变成一个可以独立回答的问题。
//
// 现状痛点：worker 写完 PoC 直接打生产目标，失败就换 payload，无从分辨是
// 「PoC 写错了」还是「目标没这个漏洞」——这是误报与无效利用的主要来源。

import (
	"context"
	"encoding/json"
	"strings"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/just805/pangu/sandbox"
)

// sandboxRuntime 是进程级单例（探测一次 docker CLI 即可，路径不会中途变）。
var sandboxRuntime = sandbox.New()

// SetSandboxRuntime 供测试替换。
func SetSandboxRuntime(r *sandbox.Runtime) { sandboxRuntime = r }

func (t *ToolSet) pocPrevalidate() actool.CoreTool {
	return t.writeExpTool("poc_prevalidate",
		"在【本地仿制环境】里先跑一遍 PoC，判断「这条 PoC 本身对不对」，再决定要不要打真目标。\n"+
			"按 tech+version 起一个同款官方镜像容器（nginx/httpd/php/redis/mysql），在容器内执行你的命令，\n"+
			"用 expect 判断是否命中预期特征。\n"+
			"**沙箱不出网**（容器只连自己）：PoC 里的目标地址一律写 `http://127.0.0.1/`（或对应端口），\n"+
			"不要写真实域名——写了会连接失败，且那属于「用沙箱当跳板」，被明确禁止。\n"+
			"**基础镜像通常没有 curl**：alpine 系（nginx/redis）用 `wget -qO- `，php:apache 有 curl，\n"+
			"起不来时先 `wget -qO- http://127.0.0.1/` 看服务在不在。\n"+
			"**结论只对仿制环境成立**：通过≠真目标可复现（配置/补丁/中间件差异仍在），\n"+
			"未通过也不是「目标没漏洞」的铁证，只是说明这条 PoC 在标准镜像上不触发——先改 PoC 再上真目标。",
		obj(map[string]any{
			"tech":        str("组件名：nginx | apache | php | redis | mysql"),
			"version":     str("目标版本（如 1.25.3 / 8.1 / 2.4 / 7.2）：用于选镜像 tag"),
			"poc_command": str("在容器内执行的命令（sh -c），目标写 127.0.0.1，如：wget -qO- http://127.0.0.1/ | head -5"),
			"expect":      str("期望命中的特征（子串，不区分大小写），如 nginx/1.25 或 root:x:0:0"),
		}, "tech", "version", "poc_command", "expect"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Tech       string `json:"tech"`
				Version    string `json:"version"`
				PocCommand string `json:"poc_command"`
				Expect     string `json:"expect"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			expect := strings.TrimSpace(a.Expect)
			if expect == "" {
				return actool.Errorf("expect 必填：没有「命中什么算成功」就无法判定，只会得到一个无法采信的结论"), nil
			}
			image, err := sandbox.ImageFor(a.Tech, a.Version)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if !sandboxRuntime.Available() {
				return actool.Errorf("本机没有可用的 docker：PoC 预验证需要能执行 docker 命令。" +
					"（Docker 部署形态自带；systemd 裸装形态需自行安装 docker 并让服务进程有权限）"), nil
			}

			res, runErr := sandboxRuntime.Run(ctx, image, a.PocCommand)
			out := map[string]any{
				"image":     image,
				"tech":      a.Tech,
				"version":   a.Version,
				"expect":    expect,
				"exit_code": -1,
				"output":    "",
			}
			if res != nil {
				out["exit_code"] = res.ExitCode
				out["output"] = res.Output
				out["timed_out"] = res.TimedOut
			}
			if runErr != nil && res == nil {
				// docker 层面失败（镜像拉不到、白名单拒绝等）：不是「PoC 不可复现」，
				// 不能因此给出负结论，所以单独标注。
				out["verdict"] = "沙箱未能执行"
				out["note"] = runErr.Error()
				return jsonResult(out)
			}

			hit := strings.Contains(strings.ToLower(res.Output), strings.ToLower(expect))
			out["hit"] = hit
			if hit {
				out["verdict"] = "PoC 在仿制环境中可复现"
				out["note"] = "再打真目标确认（真目标流量会经平台代理录制，作为正式证据）。"
			} else {
				out["verdict"] = "PoC 在仿制环境中未触发"
				out["note"] = "先改 PoC（请求路径/参数/编码/载荷），不要凭这个结论判定目标无此漏洞；" +
					"也要考虑仿制镜像与真目标的配置差异（自定义配置、补丁级别、中间件）。"
			}
			if res.TimedOut {
				out["verdict"] = "沙箱执行超时"
				out["note"] = "命令卡住了：检查是否有交互式命令、无限重试，或目标服务没起来（先单独跑一次探测）。"
			}
			return jsonResult(out)
		})
}

// sandboxAvailable 供工具目录/诊断使用。
func sandboxAvailable() bool { return sandboxRuntime.Available() }
