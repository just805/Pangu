package server

// 原生 PE 伪代码反编译（P0c）：调用 Ghidra headless（analyzeHeadless）跑内嵌脚本，
// 把每个函数的 C 伪代码导出到反编译产物目录。Ghidra 体积大、启动与自动分析慢，
// 因此本引擎走**异步**执行（见 reverseDecompile 的 ghidra 分支）。
// 样本只读，Ghidra 不会执行样本；临时工程目录执行完即删除。

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed ghidra_scripts/DecompileAll.java
var ghidraDecompileScript string

const (
	ghidraScriptName       = "DecompileAll.java"
	ghidraDecompileTimeout = 20 * time.Minute
	// 单文件自动分析上限（秒）。超过后 Ghidra 停止分析并继续执行后置脚本。
	ghidraAnalysisTimeout = "600"
)

// ghidraAnalyzeHeadless 查找 analyzeHeadless 可执行文件：
// 先看 PATH，再看 GHIDRA_HOME / GHIDRA_INSTALL_DIR 与常见安装目录。
func ghidraAnalyzeHeadless() (string, bool) {
	for _, name := range []string{"analyzeHeadless", "analyzeHeadless.bat"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	var roots []string
	for _, key := range []string{"GHIDRA_HOME", "GHIDRA_INSTALL_DIR"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			roots = append(roots, v)
		}
	}
	roots = append(roots, "/opt/ghidra", "/usr/local/ghidra", "/usr/share/ghidra")
	for _, root := range roots {
		for _, name := range []string{"analyzeHeadless", "analyzeHeadless.bat"} {
			p := filepath.Join(root, "support", name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

// runGhidraDecompile 在临时工程目录里跑一次 headless 分析 + 后置反编译脚本。
func runGhidraDecompile(ctx context.Context, samplePath, outRoot string) error {
	exe, ok := ghidraAnalyzeHeadless()
	if !ok {
		return fmt.Errorf("未找到 Ghidra analyzeHeadless（请安装 Ghidra 并把 support 目录加入 PATH）")
	}
	work, err := os.MkdirTemp("", "pangu-ghidra-*")
	if err != nil {
		return fmt.Errorf("创建 Ghidra 临时目录失败：%w", err)
	}
	defer os.RemoveAll(work) // 临时工程与脚本随本次执行删除

	projDir := filepath.Join(work, "project")
	scriptDir := filepath.Join(work, "scripts")
	for _, dir := range []string{projDir, scriptDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 Ghidra 工作目录失败：%w", err)
		}
	}
	if err := os.WriteFile(filepath.Join(scriptDir, ghidraScriptName), []byte(ghidraDecompileScript), 0o600); err != nil {
		return fmt.Errorf("写入 Ghidra 脚本失败：%w", err)
	}

	args := []string{
		projDir, "pangu",
		"-import", samplePath,
		"-analysisTimeoutPerFile", ghidraAnalysisTimeout,
		"-scriptPath", scriptDir,
		"-postScript", ghidraScriptName, outRoot,
		"-deleteProject",
	}
	var cmd *exec.Cmd
	// Windows 上 analyzeHeadless.bat 必须经 cmd.exe 启动。
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(exe), ".bat") {
		cmd = exec.CommandContext(ctx, "cmd.exe", append([]string{"/c", exe}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("反编译超时（%s）", ghidraDecompileTimeout)
	}
	if err != nil {
		return fmt.Errorf("ghidra 执行失败：%v\n%s", err, truncateError(string(out), 4000))
	}
	return nil
}
