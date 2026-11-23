package server

// 聊天/任务附件的大文件分片上传。Cloudflare 免费/Pro 单请求体 100MB、Business
// 200MB，超过上限的 exe 经 /api/chat/upload 会被边缘 413 拒绝。这里把附件切成
// 16MB 分片，逐片落到目标的 uploads/.pangu-upload-<uid> 下，完成后合并并复用
// chatUpload 的附件结构返回（Abs 供建任务时写入描述）。

import (
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
)

const (
	chatChunkSize          = 16 << 20
	chatChunkMaxIndex      = 64
	maxChatAttachmentBytes = 512 << 20
	chatUploadStaleAge     = 24 * time.Hour
)

type chatChunkMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Server) chatChunkDir(dir, uid string) string {
	return filepath.Join(dir, ".pangu-upload-"+uid)
}

// chatChunkTarget 解析 scope/id 并返回附件目录；task scope 还要占任务操作计数。
func (s *Server) chatChunkTarget(w http.ResponseWriter, r *http.Request) (dir string, release func(), ok bool) {
	var sub string
	taskScoped := false
	switch r.URL.Query().Get("scope") {
	case "task":
		sub = "tasks"
		taskScoped = true
	case "session":
		sub = "sessions"
	case "staging":
		sub = "drafts"
	default:
		writeErr(w, 400, "scope 必须是 task / session / staging")
		return "", nil, false
	}
	id := r.URL.Query().Get("id")
	if !safeChatID.MatchString(id) {
		writeErr(w, 400, "非法 id")
		return "", nil, false
	}
	release = func() {}
	if taskScoped {
		if s.m.ResolveTask(id) == nil {
			writeErr(w, 404, "task not found")
			return "", nil, false
		}
		if !s.engine.beginTaskOperation(id) {
			writeErr(w, http.StatusConflict, "任务正在删除，无法上传附件")
			return "", nil, false
		}
		release = func() { s.engine.decInflight(id) }
	}
	return filepath.Join(s.m.dir, sub, id, "uploads"), release, true
}

func readChatChunkMeta(dir string) (chatChunkMeta, error) {
	var meta chatChunkMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	if meta.Size <= 0 || meta.Size > maxChatAttachmentBytes {
		return meta, fmt.Errorf("invalid attachment size %d", meta.Size)
	}
	return meta, nil
}

func cleanupChatChunkDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".pangu-upload-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < chatUploadStaleAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
}

// chatChunkInit 创建附件分片上传会话。
func (s *Server) chatChunkInit(w http.ResponseWriter, r *http.Request) {
	dir, release, ok := s.chatChunkTarget(w, r)
	if !ok {
		return
	}
	defer release()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "建目录失败: "+err.Error())
		return
	}
	var body struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "解析上传参数失败")
		return
	}
	name := filepath.Base(strings.TrimSpace(body.Filename))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeErr(w, 400, "非法文件名")
		return
	}
	if body.Size <= 0 {
		writeErr(w, 400, "文件为空")
		return
	}
	if body.Size > maxChatAttachmentBytes {
		writeErr(w, 400, fmt.Sprintf("附件超过大小上限（%d MB）", maxChatAttachmentBytes>>20))
		return
	}
	uid, err := newReverseUploadID()
	if err != nil {
		writeErr(w, 500, "创建上传会话失败："+err.Error())
		return
	}
	cleanupChatChunkDirs(dir)
	temp := s.chatChunkDir(dir, uid)
	if err := os.MkdirAll(filepath.Join(temp, "chunks"), 0o755); err != nil {
		writeErr(w, 500, "创建上传目录失败："+err.Error())
		return
	}
	raw, _ := json.Marshal(chatChunkMeta{Name: name, Size: body.Size})
	if err := os.WriteFile(filepath.Join(temp, "meta.json"), raw, 0o600); err != nil {
		_ = os.RemoveAll(temp)
		writeErr(w, 500, "写入上传元数据失败："+err.Error())
		return
	}
	count := int((body.Size + chatChunkSize - 1) / chatChunkSize)
	writeJSON(w, 200, map[string]any{
		"upload_id":   uid,
		"chunk_size":  chatChunkSize,
		"chunk_count": count,
		"max_bytes":   maxChatAttachmentBytes,
	})
}

// chatChunkPut 接收单个分片（原始字节流）。
func (s *Server) chatChunkPut(w http.ResponseWriter, r *http.Request) {
	dir, release, ok := s.chatChunkTarget(w, r)
	if !ok {
		return
	}
	defer release()
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= chatChunkMaxIndex {
		writeErr(w, 400, "非法分片序号")
		return
	}
	temp := s.chatChunkDir(dir, uid)
	meta, err := readChatChunkMeta(temp)
	if err != nil {
		writeErr(w, 404, "上传会话不存在或已过期")
		return
	}
	count := int((meta.Size + chatChunkSize - 1) / chatChunkSize)
	if index >= count {
		writeErr(w, 400, "分片序号超出范围")
		return
	}
	want := int64(chatChunkSize)
	if index == count-1 {
		want = meta.Size - int64(index)*chatChunkSize
	}
	r.Body = http.MaxBytesReader(w, r.Body, chatChunkSize+1)
	tmp, err := os.CreateTemp(filepath.Join(temp, "chunks"), ".part-*")
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
	dest := filepath.Join(temp, "chunks", strconv.Itoa(index))
	_ = os.Remove(dest)
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, 500, "写入分片失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"index": index, "received": n})
}

// chatChunkComplete 合并分片并按 chatUpload 的命名规则落盘为附件。
func (s *Server) chatChunkComplete(w http.ResponseWriter, r *http.Request) {
	dir, release, ok := s.chatChunkTarget(w, r)
	if !ok {
		return
	}
	defer release()
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	temp := s.chatChunkDir(dir, uid)
	meta, err := readChatChunkMeta(temp)
	if err != nil {
		writeErr(w, 404, "上传会话不存在或已过期")
		return
	}
	count := int((meta.Size + chatChunkSize - 1) / chatChunkSize)
	merged := filepath.Join(temp, "merged")
	out, err := os.Create(merged)
	if err != nil {
		writeErr(w, 500, "创建合并文件失败："+err.Error())
		return
	}
	var total int64
	for i := 0; i < count; i++ {
		f, err := os.Open(filepath.Join(temp, "chunks", strconv.Itoa(i)))
		if err != nil {
			_ = out.Close()
			_ = os.Remove(merged)
			writeErr(w, 409, fmt.Sprintf("分片不完整：缺少第 %d 片", i))
			return
		}
		n, copyErr := io.Copy(out, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			_ = out.Close()
			_ = os.Remove(merged)
			writeErr(w, 500, "读取分片失败")
			return
		}
		total += n
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(merged)
		writeErr(w, 500, "写入合并文件失败："+err.Error())
		return
	}
	if total != meta.Size {
		_ = os.Remove(merged)
		writeErr(w, 400, fmt.Sprintf("合并后大小不符：期望 %d 字节，实际 %d", meta.Size, total))
		return
	}
	dest, err := saveMergedUnique(dir, meta.Name, merged)
	if err != nil {
		_ = os.Remove(merged)
		writeErr(w, 500, "保存附件失败："+err.Error())
		return
	}
	_ = os.RemoveAll(temp)
	base := filepath.Base(dest)
	writeJSON(w, 200, map[string]any{
		"attachments": []chatAttachment{{Name: base, Path: "uploads/" + base, Size: meta.Size, Abs: dest}},
	})
}

// chatChunkDelete 放弃一次分片上传会话。
func (s *Server) chatChunkDelete(w http.ResponseWriter, r *http.Request) {
	dir, release, ok := s.chatChunkTarget(w, r)
	if !ok {
		return
	}
	defer release()
	uid := r.PathValue("uid")
	if !validReverseUploadID(uid) {
		writeErr(w, 400, "非法上传会话")
		return
	}
	_ = os.RemoveAll(s.chatChunkDir(dir, uid))
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// saveMergedUnique 以 O_EXCL 语义为合并文件找不冲突的文件名（优先硬链接，
// 不支持时退化为独占复制），避免并发上传同名附件互相覆盖。
func saveMergedUnique(dir, name, src string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		dest := filepath.Join(dir, name)
		if i > 0 {
			dest = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		}
		if err := os.Link(src, dest); err == nil {
			_ = os.Remove(src)
			return dest, nil
		} else if os.IsExist(err) {
			continue
		}
		if err := copyExclusive(src, dest); err == nil {
			_ = os.Remove(src)
			return dest, nil
		} else if os.IsExist(err) {
			continue
		} else {
			return "", err
		}
	}
	return "", fmt.Errorf("同名附件过多")
}

func copyExclusive(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	return out.Close()
}
