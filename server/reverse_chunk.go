package server

// 分片上传（大样本）：Cloudflare 免费/Pro 单请求体限制 100MB、Business 200MB，
// 超过上限的 exe 无法用单请求上传。这里把样本切成固定大小分片（16MB），逐片
// 落盘后在服务端按序合并，再走与单请求上传相同的登记/分析流程。
// 所有分片位于任务 reverse/.uploads 下，随任务删除/归档一起清理。

import (
	"crypto/rand"
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
	"time"
)

const (
	// 16MB 远低于 Cloudflare 各档位的单请求体限制，也让单片上传更容易在
	// Cloudflare 的 100 秒请求超时内完成。
	reverseChunkSize      = 16 << 20
	reverseChunkMaxIndex  = 64 // 512MB / 16MB = 32，留出冗余
	reverseUploadStaleAge = 24 * time.Hour
)

type reverseUploadMeta struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

func (s *Server) reverseUploadsDir(taskID string) string {
	return filepath.Join(s.reverseSamplesDir(taskID), ".uploads")
}

func (s *Server) reverseUploadDir(taskID, uid string) string {
	return filepath.Join(s.reverseUploadsDir(taskID), uid)
}

func newReverseUploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validReverseUploadID(uid string) bool {
	if len(uid) != 32 {
		return false
	}
	_, err := hex.DecodeString(uid)
	return err == nil
}

func reverseChunkCount(size int64) int {
	if size <= 0 {
		return 0
	}
	return int((size + reverseChunkSize - 1) / reverseChunkSize)
}

func reverseChunkPath(dir string, index int) string {
	return filepath.Join(dir, "chunks", strconv.Itoa(index))
}

func readReverseUploadMeta(dir string) (reverseUploadMeta, error) {
	var meta reverseUploadMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	if meta.Size <= 0 || meta.Size > maxReverseSampleBytes {
		return meta, fmt.Errorf("invalid upload size %d", meta.Size)
	}
	return meta, nil
}

// cleanupStaleReverseUploads 清理超过 24 小时未完成的分片会话，避免磁盘泄漏。
func cleanupStaleReverseUploads(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < reverseUploadStaleAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}

// reverseChunkInit 创建一次分片上传会话。body: {"filename":"x.exe","size":123}
func (s *Server) reverseChunkInit(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if reverseTaskID(t) <= 0 {
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
	var body struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "解析上传参数失败")
		return
	}
	name := sanitizeArtifactName(body.Filename)
	if body.Size <= 0 {
		writeErr(w, 400, "样本为空文件")
		return
	}
	if body.Size > maxReverseSampleBytes {
		writeErr(w, 400, fmt.Sprintf("样本超过大小上限（%d MB）", maxReverseSampleBytes>>20))
		return
	}
	uid, err := newReverseUploadID()
	if err != nil {
		writeErr(w, 500, "创建上传会话失败："+err.Error())
		return
	}
	cleanupStaleReverseUploads(s.reverseUploadsDir(t.ID))
	dir := s.reverseUploadDir(t.ID, uid)
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0o700); err != nil {
		writeErr(w, 500, "创建上传目录失败："+err.Error())
		return
	}
	raw, _ := json.Marshal(reverseUploadMeta{Filename: name, Size: body.Size})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		writeErr(w, 500, "写入上传元数据失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"upload_id":   uid,
		"chunk_size":  reverseChunkSize,
		"chunk_count": reverseChunkCount(body.Size),
		"max_bytes":   maxReverseSampleBytes,
	})
}

// reverseChunkPut 接收单个分片（原始字节流），写入 chunks/<index>。
func (s *Server) reverseChunkPut(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if s.engine != nil {
		if !s.engine.beginTaskOperation(t.ID) {
			writeErr(w, http.StatusConflict, "任务正在删除，无法上传样本")
			return
		}
		defer s.engine.decInflight(t.ID)
	}
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= reverseChunkMaxIndex {
		writeErr(w, 400, "非法分片序号")
		return
	}
	dir := s.reverseUploadDir(t.ID, uid)
	meta, err := readReverseUploadMeta(dir)
	if err != nil {
		writeErr(w, 404, "上传会话不存在或已过期")
		return
	}
	if index >= reverseChunkCount(meta.Size) {
		writeErr(w, 400, "分片序号超出范围")
		return
	}
	want := int64(reverseChunkSize)
	if index == reverseChunkCount(meta.Size)-1 {
		want = meta.Size - int64(index)*reverseChunkSize
	}
	r.Body = http.MaxBytesReader(w, r.Body, reverseChunkSize+1)
	tmp, err := os.CreateTemp(filepath.Join(dir, "chunks"), ".part-*")
	if err != nil {
		writeErr(w, 500, "创建分片文件失败："+err.Error())
		return
	}
	tmpName := tmp.Name()
	n, copyErr := io.Copy(tmp, r.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpName)
		var maxErr *http.MaxBytesError
		if errors.As(copyErr, &maxErr) {
			writeErr(w, 400, "分片超过大小上限")
			return
		}
		writeErr(w, 500, "保存分片失败："+copyErr.Error())
		return
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		writeErr(w, 500, "保存分片失败："+closeErr.Error())
		return
	}
	if n != want {
		_ = os.Remove(tmpName)
		writeErr(w, 400, fmt.Sprintf("分片大小不符：第 %d 片应为 %d 字节，实际 %d", index, want, n))
		return
	}
	chunkPath := reverseChunkPath(dir, index)
	_ = os.Remove(chunkPath) // Windows 上 Rename 不覆盖已存在文件；重试分片时先删旧片。
	if err := os.Rename(tmpName, chunkPath); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, 500, "写入分片失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"index": index, "received": n})
}

// reverseChunkComplete 按序合并全部分片、计算哈希并登记样本。
func (s *Server) reverseChunkComplete(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if s.engine != nil {
		if !s.engine.beginTaskOperation(t.ID) {
			writeErr(w, http.StatusConflict, "任务正在删除，无法合并样本")
			return
		}
		defer s.engine.decInflight(t.ID)
	}
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	dir := s.reverseUploadDir(t.ID, uid)
	meta, err := readReverseUploadMeta(dir)
	if err != nil {
		writeErr(w, 404, "上传会话不存在或已过期")
		return
	}
	count := reverseChunkCount(meta.Size)
	samplesDir := s.reverseSamplesDir(t.ID)
	tmp, err := os.CreateTemp(samplesDir, ".chunked-*")
	if err != nil {
		writeErr(w, 500, "创建合并文件失败："+err.Error())
		return
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	var total int64
	for i := 0; i < count; i++ {
		f, err := os.Open(reverseChunkPath(dir, i))
		if err != nil {
			writeErr(w, 409, fmt.Sprintf("分片不完整：缺少第 %d 片", i))
			return
		}
		n, copyErr := io.Copy(io.MultiWriter(tmp, h), f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			writeErr(w, 500, "读取分片失败")
			return
		}
		total += n
	}
	if err := tmp.Close(); err != nil {
		writeErr(w, 500, "写入合并文件失败："+err.Error())
		return
	}
	if total != meta.Size {
		writeErr(w, 400, fmt.Sprintf("合并后大小不符：期望 %d 字节，实际 %d", meta.Size, total))
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	final := filepath.Join(samplesDir, sum)
	if _, statErr := os.Stat(final); statErr == nil {
		// 同内容样本已存在：保留已有文件，丢弃合并临时文件。
		_ = os.Remove(tmpName)
	} else if err := os.Rename(tmpName, final); err != nil {
		writeErr(w, 500, "落盘失败："+err.Error())
		return
	}
	cleanupTmp = false
	_ = os.Chmod(final, 0o600)
	_ = os.RemoveAll(dir)
	s.registerReverseSampleAsync(w, r, t, meta.Filename, sum, final, total)
}

// reverseChunkDelete 放弃一次分片上传会话。
func (s *Server) reverseChunkDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	_ = os.RemoveAll(s.reverseUploadDir(t.ID, uid))
	writeJSON(w, 200, map[string]any{"deleted": true})
}
