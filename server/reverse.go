package server

// 客户端/二进制逆向分析（P0）：用户上传样本 → 静态 triage + 敏感信息扫描。
// 样本只读、绝不执行；文件落在数据目录 reverse/<taskID>/<sha256> 下。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/reverse"
)

const (
	maxReverseSampleBytes = 512 << 20 // 单样本 512MB（与 workspace 上传上限一致）
	maxReverseFormMemory  = 32 << 20
)

func (s *Server) reverseSamplesDir(taskID string) string {
	// 放在任务目录内：任务删除/归档/还原/打包会随 data/tasks/<taskID> 一起处理，
	// 不会留下游离的恶意样本文件。
	return filepath.Join(s.m.dir, "tasks", taskID, "reverse")
}

func reverseTaskID(t *Task) int64 {
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	return id
}

// reverseUpload 接收 multipart file，落盘、哈希、登记并同步静态分析。
func (s *Server) reverseUpload(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	taskID := reverseTaskID(t)
	if taskID <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	if s.engine != nil {
		if !s.engine.beginTaskOperation(t.ID) {
			writeErr(w, http.StatusConflict, "任务正在删除，无法上传样本")
			return
		}
		defer s.engine.decInflight(t.ID)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReverseSampleBytes+1<<20)
	if err := r.ParseMultipartForm(maxReverseFormMemory); err != nil {
		writeErr(w, 400, "解析上传失败或超出大小限制："+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少上传文件（表单字段 file）")
		return
	}
	defer file.Close()

	name := sanitizeArtifactName(hdr.Filename)
	dir := s.reverseSamplesDir(t.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, 500, "创建样本目录失败："+err.Error())
		return
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		writeErr(w, 500, "创建临时文件失败："+err.Error())
		return
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(file, maxReverseSampleBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		var maxErr *http.MaxBytesError
		if errors.As(copyErr, &maxErr) {
			writeErr(w, 400, fmt.Sprintf("样本超过大小上限（%d MB）", maxReverseSampleBytes>>20))
			return
		}
		writeErr(w, 500, "保存样本失败："+copyErr.Error())
		return
	}
	if closeErr != nil {
		writeErr(w, 500, "保存样本失败："+closeErr.Error())
		return
	}
	if n > maxReverseSampleBytes {
		writeErr(w, 400, fmt.Sprintf("样本超过大小上限（%d MB）", maxReverseSampleBytes>>20))
		return
	}
	if n == 0 {
		writeErr(w, 400, "样本为空文件")
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	final := filepath.Join(dir, sum)
	if err := os.Rename(tmpName, final); err != nil {
		writeErr(w, 500, "落盘失败："+err.Error())
		return
	}
	cleanup = false
	_ = os.Chmod(final, 0o600)

	s.registerReverseSample(w, r, t, name, sum, final, n)
}

// registerReverseSample 登记已落盘的样本并同步分析。单请求上传与分片合并共用，
// 保证两条路径的响应结构、去重与错误处理一致。
func (s *Server) registerReverseSample(w http.ResponseWriter, r *http.Request, t *Task, name, sum, final string, size int64) {
	sample := &db.ReverseSample{
		TaskID:      reverseTaskID(t),
		Filename:    name,
		SHA256:      sum,
		SizeBytes:   size,
		Status:      "uploaded",
		StoragePath: final,
	}
	inserted, err := s.m.pg.InsertReverseSample(r.Context(), sample)
	if err != nil {
		_ = os.Remove(final)
		writeErr(w, 500, "登记样本失败："+err.Error())
		return
	}
	if !inserted && sample.Status == "analyzed" {
		// 同任务同内容已分析过：直接复用结果，避免重复扫描。
		secrets, _ := s.m.pg.ListReverseSecrets(sample.ID)
		writeJSON(w, 200, map[string]any{"sample": sample, "secrets": secrets, "duplicate": true})
		return
	}
	if !inserted && sample.Status == "analyzing" {
		writeJSON(w, 200, map[string]any{"sample": sample, "secrets": []db.ReverseSecret{}, "analyzing": true, "duplicate": true})
		return
	}
	s.runReverseAnalysis(r.Context(), sample)
	fresh, _ := s.m.pg.GetReverseSample(sample.ID)
	if fresh != nil {
		sample = fresh
	}
	secrets, _ := s.m.pg.ListReverseSecrets(sample.ID)
	writeJSON(w, 200, map[string]any{"sample": sample, "secrets": secrets})
}

// registerReverseSampleAsync 用于分片上传：合并后立即返回，静态分析放到后台。
// 大样本分析可能超过 Cloudflare 的 100 秒响应超时，必须先把请求收尾；前端轮询
// 样本状态（analyzing → analyzed/failed）。
func (s *Server) registerReverseSampleAsync(w http.ResponseWriter, r *http.Request, t *Task, name, sum, final string, size int64) {
	sample := &db.ReverseSample{
		TaskID:      reverseTaskID(t),
		Filename:    name,
		SHA256:      sum,
		SizeBytes:   size,
		Status:      "analyzing",
		StoragePath: final,
	}
	inserted, err := s.m.pg.InsertReverseSample(r.Context(), sample)
	if err != nil {
		_ = os.Remove(final)
		writeErr(w, 500, "登记样本失败："+err.Error())
		return
	}
	if !inserted && sample.Status == "analyzed" {
		secrets, _ := s.m.pg.ListReverseSecrets(sample.ID)
		writeJSON(w, 200, map[string]any{"sample": sample, "secrets": secrets, "duplicate": true})
		return
	}
	if !inserted && sample.Status == "analyzing" {
		writeJSON(w, 200, map[string]any{"sample": sample, "secrets": []db.ReverseSecret{}, "analyzing": true, "duplicate": true})
		return
	}
	sample.Status = "analyzing"
	_ = s.m.pg.UpdateReverseSampleAnalysis(sample.ID, sample.Kind, "analyzing", sample.Metadata, "")
	taskID := t.ID
	go func(id int64) {
		if s.engine != nil {
			if !s.engine.beginTaskOperation(taskID) {
				return
			}
			defer s.engine.decInflight(taskID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		fresh, err := s.m.pg.GetReverseSample(id)
		if err != nil || fresh == nil {
			return
		}
		s.runReverseAnalysis(ctx, fresh)
	}(sample.ID)
	writeJSON(w, 200, map[string]any{"sample": sample, "secrets": []db.ReverseSecret{}, "analyzing": true})
}

func (s *Server) runReverseAnalysis(ctx context.Context, sample *db.ReverseSample) {
	res, err := reverse.Analyze(sample.StoragePath, sample.Filename)
	if err != nil {
		sample.Status = "failed"
		sample.Error = err.Error()
		_ = s.m.pg.UpdateReverseSampleAnalysis(sample.ID, sample.Kind, "failed", json.RawMessage(`{}`), err.Error())
		return
	}
	meta, err := json.Marshal(res.Meta)
	if err != nil {
		meta = json.RawMessage(`{}`)
	}
	if err := s.m.pg.UpdateReverseSampleAnalysis(sample.ID, res.Kind, "analyzed", meta, ""); err != nil {
		sample.Status = "failed"
		sample.Error = "保存分析结果失败：" + err.Error()
		return
	}
	secrets := make([]db.ReverseSecret, 0, len(res.Secrets))
	for _, x := range res.Secrets {
		secrets = append(secrets, db.ReverseSecret{
			SampleID:   sample.ID,
			Rule:       x.Rule,
			Match:      x.Match,
			Context:    x.Context,
			Offset:     x.Offset,
			Section:    x.Section,
			Confidence: x.Confidence,
		})
	}
	if err := s.m.pg.ReplaceReverseSecrets(ctx, sample.ID, "binary", secrets); err != nil {
		_ = s.m.pg.UpdateReverseSampleAnalysis(sample.ID, res.Kind, "failed", meta, "保存敏感信息失败："+err.Error())
		sample.Status = "failed"
		sample.Error = "保存敏感信息失败：" + err.Error()
		return
	}
	sample.Kind = res.Kind
	sample.Status = "analyzed"
	sample.Metadata = meta
	sample.Error = ""
	sample.SecretCount = len(secrets)
}

func (s *Server) reverseList(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	samples, err := s.m.pg.ListReverseSamples(reverseTaskID(t))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if samples == nil {
		samples = []*db.ReverseSample{}
	}
	writeJSON(w, 200, map[string]any{"samples": samples})
}

func (s *Server) reverseGet(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	secrets, err := s.m.pg.ListReverseSecrets(sample.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if secrets == nil {
		secrets = []db.ReverseSecret{}
	}
	writeJSON(w, 200, map[string]any{"sample": sample, "secrets": secrets})
}

func (s *Server) reverseAnalyze(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	if _, err := os.Stat(sample.StoragePath); err != nil {
		sample.Status = "failed"
		sample.Error = "样本文件不存在，请重新上传"
		_ = s.m.pg.UpdateReverseSampleAnalysis(sample.ID, sample.Kind, "failed", sample.Metadata, sample.Error)
		writeJSON(w, 200, map[string]any{"sample": sample, "secrets": []db.ReverseSecret{}})
		return
	}
	s.runReverseAnalysis(r.Context(), sample)
	fresh, _ := s.m.pg.GetReverseSample(sample.ID)
	if fresh != nil {
		sample = fresh
	}
	secrets, _ := s.m.pg.ListReverseSecrets(sample.ID)
	if secrets == nil {
		secrets = []db.ReverseSecret{}
	}
	writeJSON(w, 200, map[string]any{"sample": sample, "secrets": secrets})
}

func (s *Server) reverseDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if sid <= 0 {
		writeErr(w, 400, "bad sample id")
		return
	}
	path, err := s.m.pg.DeleteReverseSample(reverseTaskID(t), sid)
	if err != nil {
		writeErr(w, 404, "sample not found")
		return
	}
	if path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.m.dir, filepath.Clean(path))
		}
		_ = os.Remove(path)
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "id": sid})
}

func (s *Server) reverseDownload(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	sample, ok := s.reverseSampleForTask(w, t, r.PathValue("sid"))
	if !ok {
		return
	}
	f, err := os.Open(sample.StoragePath)
	if err != nil {
		writeErr(w, 404, "样本文件不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", attachmentDisposition(sample.Filename))
	size := sample.SizeBytes
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, f)
}

// reverseSampleForTask 解析 sid 并校验样本属于当前任务；失败时已写响应。
func (s *Server) reverseSampleForTask(w http.ResponseWriter, t *Task, rawID string) (*db.ReverseSample, bool) {
	sid, _ := strconv.ParseInt(rawID, 10, 64)
	if sid <= 0 {
		writeErr(w, 400, "bad sample id")
		return nil, false
	}
	sample, err := s.m.pg.GetReverseSample(sid)
	if err != nil || sample == nil {
		writeErr(w, 404, "sample not found")
		return nil, false
	}
	if sample.TaskID != reverseTaskID(t) {
		writeErr(w, 404, "sample not found")
		return nil, false
	}
	// 历史记录里可能存的是相对路径；统一按数据目录解析。
	if sample.StoragePath != "" && !filepath.IsAbs(sample.StoragePath) {
		sample.StoragePath = filepath.Join(s.m.dir, filepath.Clean(sample.StoragePath))
	}
	if strings.TrimSpace(sample.Filename) == "" {
		sample.Filename = "sample.bin"
	}
	return sample, true
}
