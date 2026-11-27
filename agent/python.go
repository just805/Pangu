package agent

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// noPythonHint 是「本机没有 Python 3」时给模型的可执行替代路径。
// 措辞指向具体能做的事（文件模式），而不是只报告缺失。
const noPythonHint = "本机没有可用的 Python 3，这条「组命令」需要一个解释器才能跑差分/解析脚本。"

// errNoPython 供组命令函数返回，工具层据此换成带替代路径的提示。
var errNoPython = errors.New(noPythonHint)

// pythonInterpreter 返回本机可用的 Python 3 命令名（"python3" / "python"），
// 都没有则返回空串。
//
// 为什么不能硬编码 python3：Windows 官方安装器默认只装 python.exe，没有
// python3.exe；api_contract 与 diff_responses 的 URL 模式此前直接生成
// `python3 …`，在 Windows 上表现为 `exit status 9009`（命令不存在）——
// 对使用者是一条没有信息量的死路。
//
// 为什么不止 LookPath：Windows 10+ 的 "python" 可能只是 Microsoft Store 的
// 应用执行别名，LookPath 能找到、执行时却拉起商店并失败。所以真跑一次
// `-c` 验版本，只认 Python 3。sync.OnceValue 缓存结果，只付一次探测成本。
//
// 注意：server/customtool.go 的 detectPython 是同一套判定，但返回绝对路径
// （自定义工具直接 exec 它）。两处改动必须同步，否则「拼命令用命令名、
// 直接执行用绝对路径」会出现一边能用另一边不能用的割裂。
var pythonInterpreter = sync.OnceValue(func() string {
	for _, name := range []string{"python3", "python"} {
		if _, err := exec.LookPath(name); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := exec.CommandContext(ctx, name,
			"-c", "import sys; sys.exit(0 if sys.version_info[0] >= 3 else 1)").Run()
		cancel()
		if err == nil {
			return name
		}
	}
	return ""
})

// pythonMissingMessage 组装「缺解释器」时给模型的完整提示：说清缺什么、
// 为什么原来的路走不通、以及现在就能走的替代方案。
func pythonMissingMessage(alternatives string) string {
	return noPythonHint + "改用不需要解释器的路径：" + alternatives +
		"（或安装 Python 3 并确保其命令在 PATH 中，之后 URL 模式即可用。）"
}
