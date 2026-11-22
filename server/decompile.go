package server

// 反编译（P0b）：对已上传样本做静态反编译，产出源码/伪代码供人工与 agent 审计。
// 支持：
//   - .NET PE   → ilspycmd（需宿主安装）
//   - Electron  → @electron/asar 解包（Node/npm 已随镜像提供）
//   - APK/JAR   → jadx（移动端技能已依赖，宿主环境页可装）
//   - 原生 PE   → Ghidra headless（异步执行，见 ghidra.go）
// 样本只读，绝不执行；输出目录随任务目录一起删除/归档。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/reverse"
)

const (
	decompileTimeout    = 3 * time.Minute
	maxDecompileFiles   = 5000
	maxDecompileBytes   = 200 << 20
	maxDecompiledSource = 1 << 20 // 单文件读取/展示上限 1MB
	maxDecompileSecrets = 500
)

type decompileFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Text bool   `json:"text"`
}

// reverseDecompile 触发一次反编译。body: {"engine":"auto|ilspy|asar|jadx|ghidra"}
func (s *Server) reverseDecompile(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	// 任务操作计数：同步引擎在 handler 返回时释放；ghidra 异步执行时转交给后台 goroutine。
	release := func() {}
	if s.engine != nil {
		if !s.engine.beginTaskOperation(t.ID) {
			writeErr(w, http.StatusConflict, "任务正在删除，无法反编译")
			return
		}
		release = func() { s.engine.decInflight(t.ID) }
	}
	var body struct {
		Engine string `json:"engine"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body)

	engine, err := s.decompileEngineFor(sample, body.Engine)
	if err != nil {
		release()
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	if hint := decompileInstallHint(engine); hint != "" && !decompileToolAvailable(engine) {
		release()
		writeJSON(w, 409, map[string]any{
			"error":   fmt.Sprintf("%s 未安装，无法反编译", decompileEngineCN(engine)),
			"install": hint,
			"engine":  engine,
		})
		return
	}
	if sample.DecompileStatus == "running" {
		release()
		writeJSON(w, 409, map[string]any{"error": "该样本正在反编译中，请稍后刷新", "engine": sample.DecompileEngine})
		return
	}

	outRoot := filepath.Join(s.reverseSamplesDir(t.ID), "decompiled", sample.SHA256, engine)
	_ = os.RemoveAll(outRoot)
	if err := os.MkdirAll(outRoot, 0o700); err != nil {
		release()
		writeErr(w, 500, "创建反编译目录失败："+err.Error())
		return
	}
	_ = s.m.pg.UpdateReverseSampleDecompile(sample.ID, engine, "running", outRoot, "")

	if engine == "ghidra" {
		// Ghidra 启动 + 自动分析 + 逐函数反编译通常需要数分钟，不能占着 HTTP 连接。
		// 立即返回 202，前端轮询 /decompiled 状态；inflight 计数由 goroutine 收尾时释放。
		go s.runGhidraDecompileAsync(sample.ID, sample.StoragePath, engine, outRoot, release)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"sample": mustReverseSample(s.m.pg, sample.ID, sample),
			"files":  []decompileFile{},
			"engine": engine,
			"status": "running",
		})
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(r.Context(), decompileTimeout)
	defer cancel()
	if err := runDecompileCommand(ctx, engine, sample.StoragePath, outRoot); err != nil {
		msg := truncateError(err.Error(), 4000)
		_ = s.m.pg.UpdateReverseSampleDecompile(sample.ID, engine, "failed", outRoot, msg)
		writeJSON(w, 200, map[string]any{
			"sample": mustReverseSample(s.m.pg, sample.ID, sample),
			"files":  []decompileFile{},
			"error":  msg,
			"engine": engine,
		})
		return
	}

	files, total, err := listDecompileFiles(outRoot)
	if err != nil {
		msg := "读取反编译产物失败：" + err.Error()
		_ = s.m.pg.UpdateReverseSampleDecompile(sample.ID, engine, "failed", outRoot, msg)
		writeJSON(w, 200, map[string]any{"sample": mustReverseSample(s.m.pg, sample.ID, sample), "files": []decompileFile{}, "error": msg, "engine": engine})
		return
	}
	if len(files) == 0 {
		msg := "反编译命令执行成功但没有产出文件"
		_ = s.m.pg.UpdateReverseSampleDecompile(sample.ID, engine, "failed", outRoot, msg)
		writeJSON(w, 200, map[string]any{"sample": mustReverseSample(s.m.pg, sample.ID, sample), "files": []decompileFile{}, "error": msg, "engine": engine})
		return
	}
	secretCount := s.scanDecompiledSecrets(r.Context(), sample, outRoot, files)
	_ = s.m.pg.UpdateReverseSampleDecompile(sample.ID, engine, "done", outRoot, "")
	writeJSON(w, 200, map[string]any{
		"sample":       mustReverseSample(s.m.pg, sample.ID, sample),
		"files":        files,
		"total_bytes":  total,
		"engine":       engine,
		"secret_count": secretCount,
	})
}

// runGhidraDecompileAsync 后台执行 Ghidra 反编译，结束后落库最终状态并释放 inflight。
func (s *Server) runGhidraDecompileAsync(sampleID int64, samplePath, engine, outRoot string, release func()) {
	defer func() {
		if rec := recover(); rec != nil {
			_ = s.m.pg.UpdateReverseSampleDecompile(sampleID, engine, "failed", outRoot, fmt.Sprintf("反编译异常：%v", rec))
		}
		release()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), ghidraDecompileTimeout)
	defer cancel()
	if err := runDecompileCommand(ctx, engine, samplePath, outRoot); err != nil {
		_ = s.m.pg.UpdateReverseSampleDecompile(sampleID, engine, "failed", outRoot, truncateError(err.Error(), 4000))
		return
	}
	files, _, err := listDecompileFiles(outRoot)
	if err != nil {
		_ = s.m.pg.UpdateReverseSampleDecompile(sampleID, engine, "failed", outRoot, "读取反编译产物失败："+err.Error())
		return
	}
	if len(files) == 0 {
		_ = s.m.pg.UpdateReverseSampleDecompile(sampleID, engine, "failed", outRoot, "反编译命令执行成功但没有产出文件")
		return
	}
	// 反编译本身可能耗尽 20 分钟预算，扫描单独给一个短超时。
	scanCtx, cancelScan := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelScan()
	s.scanDecompiledSecrets(scanCtx, &db.ReverseSample{ID: sampleID}, outRoot, files)
	_ = s.m.pg.UpdateReverseSampleDecompile(sampleID, engine, "done", outRoot, "")
}

func mustReverseSample(pg *db.DB, id int64, fallback *db.ReverseSample) *db.ReverseSample {
	if fresh, err := pg.GetReverseSample(id); err == nil && fresh != nil {
		return fresh
	}
	return fallback
}

// decompileEngineFor 自动选择引擎；用户显式指定时尊重用户选择。
func (s *Server) decompileEngineFor(sample *db.ReverseSample, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" && requested != "auto" {
		switch requested {
		case "ilspy", "asar", "jadx", "ghidra":
			return requested, nil
		default:
			return "", fmt.Errorf("不支持的反编译引擎：%s", requested)
		}
	}
	var meta reverse.Meta
	_ = json.Unmarshal(sample.Metadata, &meta)
	switch {
	case meta.DotNet:
		return "ilspy", nil
	case sample.Kind == "electron-asar":
		return "asar", nil
	case sample.Kind == "apk" || sample.Kind == "jar":
		return "jadx", nil
	case sample.Kind == "pe":
		return "ghidra", nil
	default:
		return "", fmt.Errorf("样本类型 %s 暂不支持反编译", sample.Kind)
	}
}

func decompileEngineCN(engine string) string {
	switch engine {
	case "ilspy":
		return "ILSpy（.NET）"
	case "asar":
		return "Electron asar"
	case "jadx":
		return "JADX"
	case "ghidra":
		return "Ghidra"
	default:
		return engine
	}
}

func decompileToolAvailable(engine string) bool {
	switch engine {
	case "ilspy":
		_, err := exec.LookPath("ilspycmd")
		return err == nil
	case "asar":
		if _, err := exec.LookPath("asar"); err == nil {
			return true
		}
		_, err := exec.LookPath("npx")
		return err == nil
	case "jadx":
		_, err := exec.LookPath("jadx")
		return err == nil
	case "ghidra":
		_, ok := ghidraAnalyzeHeadless()
		return ok
	}
	return false
}

func decompileInstallHint(engine string) string {
	switch engine {
	case "ilspy":
		return "安装 .NET SDK 后执行：dotnet tool install --global ilspycmd，并确保 ~/.dotnet/tools 在 PATH 中（宿主环境页可查看运行时状态）。"
	case "asar":
		return "执行：npm install -g @electron/asar（镜像已带 Node 20；也可在宿主环境页安装）。"
	case "jadx":
		return "在「宿主环境」页安装 jadx（移动端逆向分类）。"
	case "ghidra":
		return "原生 PE 伪代码反编译需要 Ghidra（约 1GB）：解压到 /opt/ghidra 并把 support/analyzeHeadless 加入 PATH，或设置 GHIDRA_HOME；「宿主环境」页可查看检测状态。"
	}
	return ""
}

func runDecompileCommand(ctx context.Context, engine, samplePath, outRoot string) error {
	if engine == "ghidra" {
		return runGhidraDecompile(ctx, samplePath, outRoot)
	}
	if engine == "ilspy" {
		// -p 输出可编译工程；失败时退回单文件模式（部分老版本/混淆程序不支持工程模式）。
		cmd := exec.CommandContext(ctx, "ilspycmd", "-p", "-o", outRoot, samplePath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		_ = os.RemoveAll(outRoot)
		if err := os.MkdirAll(outRoot, 0o700); err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, "ilspycmd", "-o", outRoot, samplePath)
		out2, err2 := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("反编译超时（%s）", decompileTimeout)
		}
		if err2 != nil {
			return fmt.Errorf("ilspycmd 执行失败：%v\n%s\n%s", err2, truncateError(string(out), 2000), truncateError(string(out2), 2000))
		}
		return nil
	}
	var cmd *exec.Cmd
	switch engine {
	case "asar":
		if _, err := exec.LookPath("asar"); err == nil {
			cmd = exec.CommandContext(ctx, "asar", "extract", samplePath, outRoot)
		} else {
			cmd = exec.CommandContext(ctx, "npx", "--yes", "@electron/asar", "extract", samplePath, outRoot)
		}
	case "jadx":
		cmd = exec.CommandContext(ctx, "jadx", "-d", outRoot, samplePath)
	default:
		return fmt.Errorf("未实现的反编译引擎：%s", engine)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("反编译超时（%s）", decompileTimeout)
	}
	if err != nil {
		return fmt.Errorf("%s 执行失败：%v\n%s", engine, err, truncateError(string(out), 4000))
	}
	return nil
}

func listDecompileFiles(root string) ([]decompileFile, int64, error) {
	var files []decompileFile
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if len(files) >= maxDecompileFiles || total >= maxDecompileBytes {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, decompileFile{Path: filepath.ToSlash(rel), Size: info.Size(), Text: isTextSource(rel)})
		total += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, total, err
}

func isTextSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cs", ".java", ".kt", ".js", ".ts", ".jsx", ".tsx", ".json", ".xml", ".config",
		".txt", ".html", ".htm", ".csproj", ".sln", ".resx", ".xaml", ".yml", ".yaml",
		".properties", ".map", ".smali", ".gradle", ".md",
		".c", ".h", ".cpp", ".hpp", ".cc", ".inc":
		return true
	}
	return false
}

// scanDecompiledSecrets 对反编译出的文本做二次规则扫描，按文件路径落 section。
func (s *Server) scanDecompiledSecrets(ctx context.Context, sample *db.ReverseSample, root string, files []decompileFile) int {
	var secrets []db.ReverseSecret
	for _, f := range files {
		if !f.Text || f.Size > maxDecompiledSource || len(secrets) >= maxDecompileSecrets {
			continue
		}
		abs, ok := safeDecompilePath(root, f.Path)
		if !ok {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, hit := range reverse.ScanText(string(data)) {
			secrets = append(secrets, db.ReverseSecret{
				SampleID:   sample.ID,
				Rule:       hit.Rule,
				Match:      hit.Match,
				Context:    hit.Context,
				Offset:     hit.Offset,
				Section:    f.Path,
				Source:     "decompile",
				Confidence: hit.Confidence,
			})
			if len(secrets) >= maxDecompileSecrets {
				break
			}
		}
	}
	if err := s.m.pg.ReplaceReverseSecrets(ctx, sample.ID, "decompile", secrets); err != nil {
		return 0
	}
	return len(secrets)
}

// reverseDecompiled 返回产物文件清单与状态。
func (s *Server) reverseDecompiled(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	out := map[string]any{
		"status": sample.DecompileStatus,
		"engine": sample.DecompileEngine,
		"error":  sample.DecompileError,
		"files":  []decompileFile{},
	}
	if sample.DecompileDir == "" {
		writeJSON(w, 200, out)
		return
	}
	root := s.resolveDecompileDir(t, sample)
	files, total, err := listDecompileFiles(root)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	out["files"] = files
	out["total_bytes"] = total
	writeJSON(w, 200, out)
}

// reverseDecompiledFile 返回单个源码文件内容（仅文本、≤1MB、路径限制在产物目录内）。
func (s *Server) reverseDecompiledFile(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeErr(w, 400, "缺少 path")
		return
	}
	if !isTextSource(rel) {
		writeErr(w, 415, "该文件不是可展示的文本源码")
		return
	}
	root := s.resolveDecompileDir(t, sample)
	abs, ok := safeDecompilePath(root, rel)
	if !ok {
		writeErr(w, 400, "非法路径")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, 404, "文件不存在")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > maxDecompiledSource {
		writeErr(w, 413, "文件超过展示上限（1MB）")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(f, maxDecompiledSource))
}

func (s *Server) resolveDecompileDir(t *Task, sample *db.ReverseSample) string {
	root := sample.DecompileDir
	if root == "" {
		return ""
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.m.dir, filepath.Clean(root))
	}
	// 防御：产物目录必须落在本任务 reverse 目录内。
	base := s.reverseSamplesDir(t.ID)
	if rel, err := filepath.Rel(base, root); err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return root
}

func safeDecompilePath(root, rel string) (string, bool) {
	if root == "" {
		return "", false
	}
	// 拒绝绝对路径（Windows 下 filepath.IsAbs("/etc/passwd") 为 false，需同时看前缀）。
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", false
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return "", false
	}
	abs := filepath.Join(root, clean)
	back, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(back, "..") {
		return "", false
	}
	return abs, true
}

func truncateError(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
