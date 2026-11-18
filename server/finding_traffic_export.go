package server

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/evidence"
	"github.com/just805/pangu/report"
)

// redactedHeaders 是导出脱敏命中的头名（大小写不敏感，按 HTTP 头名比较）。
// 导出物（md-zip 证据包）会外发给委托方，MITM 留存的目标会话凭据
// （Authorization/Cookie 等）不应随包外泄——头名与请求形状保留，证据链仍可读、
// 可复核，只是值不带走。
var redactedHeaders = map[string]bool{
	"Authorization":       true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"Proxy-Authorization": true,
	"X-Api-Key":           true,
}

// redactHead 对原始请求/响应头块做行级脱敏：命中敏感头名的行把值替换为
// [REDACTED]。头块是原始 wire 格式（\r\n 或 \n 分行），按行前缀匹配即可。
func redactHead(head string) string {
	if head == "" {
		return head
	}
	lines := strings.Split(head, "\n")
	for i, ln := range lines {
		if c := strings.Index(ln, ":"); c > 0 && redactedHeaders[strings.TrimSpace(ln[:c])] {
			lines[i] = ln[:c] + ": [REDACTED]"
		}
	}
	return strings.Join(lines, "\n")
}

func writeFindingsEvidenceZip(out io.Writer, findings []*db.DBFinding, stage string, now time.Time, redact bool) error {
	zw := zip.NewWriter(out)
	if err := writeFindingsZipEntries(zw, findings, stage, now, redact); err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

func writeFindingsZipEntries(zw *zip.Writer, findings []*db.DBFinding, stage string, now time.Time, redact bool) error {
	store := evidence.New(nil, nil, stage) // private export copy; not subject to GC
	used := map[string]int{}
	for _, f := range findings {
		base := report.FindingFilename(f)
		name := base
		if n := used[base]; n > 0 {
			name = fmt.Sprintf("%s-%d.md", strings.TrimSuffix(base, ".md"), n+1)
		}
		used[base]++
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		// 带 BOM：Windows 记事本/部分编辑器默认按 ANSI(GBK) 打开无 BOM 的 UTF-8，
		// 中文正文会整段乱码。Markdown 本身允许 BOM。
		if _, err = io.WriteString(w, "\xef\xbb\xbf"+report.SingleFindingMarkdown(f, now)); err != nil {
			return err
		}
		for _, b := range f.TrafficBindings {
			prefix := fmt.Sprintf("evidence/%d/%d/", f.ID, b.ID)
			w, err := zw.Create(prefix + "manifest.json")
			if err != nil {
				return err
			}
			if err = json.NewEncoder(w).Encode(b); err != nil {
				return err
			}
			for _, side := range []string{"request", "response"} {
				if err := func() error {
					body, _, err := store.OpenBody(b.Snapshot, side)
					if err != nil {
						return err
					}
					defer body.Close()
					w, err := zw.Create(prefix + side + ".http")
					if err != nil {
						return err
					}
					head := b.Snapshot.ReqHead
					if side == "response" {
						head = b.Snapshot.RespHead
					}
					if redact {
						head = redactHead(head)
					}
					if _, err = io.WriteString(w, strings.TrimRight(head, "\r\n")+"\n\n"); err != nil {
						return err
					}
					if _, err = io.Copy(w, body); err != nil {
						return err
					}
					if _, err = body.Seek(0, io.SeekStart); err != nil {
						return err
					}
					w, err = zw.Create(prefix + side + ".bin")
					if err != nil {
						return err
					}
					_, err = io.Copy(w, body)
					return err
				}(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func buildFindingsEvidenceZip(path string, findings []*db.DBFinding, stage string, now time.Time, redact bool) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := writeFindingsEvidenceZip(f, findings, stage, now, redact)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// Re-read every compressed entry to validate the finished ZIP, including CRCs,
	// before the caller starts an HTTP download response.
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		r, err := entry.Open()
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, r)
		if err := errors.Join(readErr, r.Close()); err != nil {
			return err
		}
	}
	return nil
}
