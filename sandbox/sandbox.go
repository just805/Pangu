// Package sandbox 是「PoC 预验证」的单机 Docker 沙箱。
//
// ## 它解决什么
//
// 现状：worker 写完 PoC 直接打生产目标，失败就换 payload，拿不到「这个 PoC 到底
// 对不对」的独立判断——这是误报与无效利用的主要来源。沙箱提供一次「先在一个
// 同款的干净环境里跑通」的机会。
//
// ## 它不是什么
//
//   - 不是全工具隔离（那要改造 94 件宿主工具的执行路径，代价与收益不成比例）；
//   - 不是编排平台（不引 K8s/Nomad，只调 docker CLI）；
//   - 不是靶场平台（只服务「验证一个 PoC」，不做可复用的漏洞环境库）。
//
// ## 首期关键取舍：--network none
//
// 设计稿要求「容器出站走平台代理以便录制」。首期**刻意不这么做**：
// 沙箱验证的是「PoC 逻辑对不对」，打的是容器内自己的 127.0.0.1 服务，
// 本来就没有「真目标流量」可录；而给了网络出口就等于给了跳板能力
// （用沙箱当代理去打未授权目标），这条风险远大于「沙箱内流量没被留痕」的损失。
//
// 真目标的流量在「沙箱验证通过 → 打真目标」那一步照常经 MITM 录制，证据链完整。
// 等将来确有「PoC 必须访问外部依赖」的需求时再评估放行，且那时要配合出口白名单。
//
// ## 已实测的拓扑结论（2026-11-27，P0）
//
// 容器 → 宿主 MITM 的路径是通的（HTTP_PROXY + host.docker.internal），
// 前提是 MITM 绑 0.0.0.0（绑 127.0.0.1 时容器不可达）。这一点记录在此，
// 供将来做「带录制的沙箱」时直接复用，不必重新踩一遍。
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrUnavailable 表示本机没有可用的 docker CLI（非 Docker 部署、或 PATH 里没有）。
var ErrUnavailable = errors.New("本机没有可用的 docker CLI：计预验证沙箱需要能执行 docker 命令")

// limits 是硬约束，不是可选项：一个失控的 PoC（fork 炸弹、内存耗尽）会打垮宿主。
const (
	maxMemory  = "512m"
	maxCPUs    = "1"
	maxPids    = "128"
	execTimout = 90 * time.Second
	outputCap  = 16 << 10
)

// allowedImagePrefixes 是镜像白名单。只允许官方基础镜像的固定前缀，
// 且镜像名里不许出现 "/"（挡住私有仓库与任意仓库）。
//
// 这条同时是安全核心：禁止任意 docker run，就消掉了「用沙箱当跳板 / 拉恶意镜像」。
var allowedImagePrefixes = []string{
	"nginx:", "httpd:", "php:", "redis:", "mysql:", "postgres:", "tomcat:", "openjdk:", "maven:",
}

// Runtime 通过 docker CLI 执行一次性容器。
type Runtime struct {
	docker string
}

// New 探测 docker CLI。返回的 Runtime 即使不可用也安全（调用会返回 ErrUnavailable）。
func New() *Runtime {
	p, err := exec.LookPath("docker")
	if err != nil {
		return &Runtime{}
	}
	return &Runtime{docker: p}
}

// Available 报告本机是否可执行 docker。
func (r *Runtime) Available() bool { return r != nil && r.docker != "" }

// ImageAllowed 校验镜像是否在白名单内。
func ImageAllowed(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "/") {
		return false
	}
	for _, p := range allowedImagePrefixes {
		if strings.HasPrefix(image, p) && len(image) > len(p) {
			return true
		}
	}
	return false
}

// RunResult 是一次沙箱执行的观测结果。
type RunResult struct {
	Image    string `json:"image"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// Run 起一个一次性容器执行 cmd（经 sh -c），跑完自动销毁。
//
// 固定 --network none：容器只能访问自己，不能出网（见包注释的取舍说明）。
func (r *Runtime) Run(ctx context.Context, image, cmd string) (*RunResult, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if !ImageAllowed(image) {
		return nil, fmt.Errorf("镜像 %q 不在白名单内：只允许官方基础镜像（%s）",
			image, strings.Join(allowedImagePrefixes, " "))
	}
	if strings.TrimSpace(cmd) == "" {
		return nil, fmt.Errorf("PoC 命令不能为空")
	}
	bs := bootstrapFor(image)
	if bs == "" {
		return nil, fmt.Errorf("%s 的仿制环境暂不支持（无法自动起服务）：PoC 会在一个「什么都没在跑」的"+
			"容器里执行，得到的负结论没有意义。当前支持 nginx / apache / php / redis", image)
	}

	runCtx, cancel := context.WithTimeout(ctx, execTimout)
	defer cancel()

	// --entrypoint sh 会覆盖镜像自带的启动命令，所以先在容器内把服务拉起来再跑 PoC。
	// mysql 这类需要初始化数据目录的镜像不支持自动起服务（见 bootstrapFor）。
	script := bs + " ; sleep 1 ; " + cmd

	args := []string{
		"run", "--rm",
		"--network", "none", // 不出网：见包注释
		"--memory", maxMemory,
		"--cpus", maxCPUs,
		"--pids-limit", maxPids,
		"--entrypoint", "sh",
		image, "-c", script,
	}
	c := exec.CommandContext(runCtx, r.docker, args...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()

	res := &RunResult{Image: image, Output: clip(buf.String(), outputCap)}
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, fmt.Errorf("沙箱执行超过 %s 被终止（PoC 可能卡在交互/重试上，或目标服务没起来）", execTimout)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode() // 非零退出是「PoC 没成功」的常见形态，不算工具错误
	default:
		return res, fmt.Errorf("执行 docker 失败：%w", err)
	}
	return res, nil
}

// clip 截断输出，保留尾部——PoC 的失败原因通常在最后几行。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…（已截断前段）\n" + s[len(s)-n:]
}
