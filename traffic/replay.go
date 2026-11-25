package traffic

// N3 http_replay：从已录制流量里挑一条请求，改参数重放——authz 测试的原子操作
// （同一个 URL 换身份/换参数回读）。请求原文来自 exchange_bodies，出口与录制
// 同源（捕获开=记录代理，重放本身也会被录制留痕；隐藏服务按 host 分流）。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/just805/pangu/proxyprobe"
)

// replayRequest 从录制库取一条请求的原始头与正文。大正文用 blob 引用时返回
// 提示——重放需要完整正文，截断的录制条目拒绝重放（避免误判目标响应）。
func (t *Traffic) replayRequest(id string) (method, rawURL string, header http.Header, body []byte, err error) {
	var head string
	var reqBody []byte
	var reqBlob sql.NullString
	err = t.db.QueryRow(`SELECT COALESCE(e.method,''), COALESCE(e.url,''), b.req_head, b.req_body, b.req_blob
FROM exchanges e LEFT JOIN exchange_bodies b ON b.id=e.id WHERE e.id=?`, id).
		Scan(&method, &rawURL, &head, &reqBody, &reqBlob)
	if err != nil {
		return "", "", nil, nil, err
	}
	if reqBlob.Valid && reqBlob.String != "" {
		return "", "", nil, nil, fmt.Errorf("该请求的正文是截断的 blob 引用（sha256:%s），无法完整重放；换一条未截断的记录", reqBlob.String)
	}
	// req_head 是 "METHOD path HTTP/1.1\r\nHost: ...\r\n..." 形态的原始请求头。
	br := bufio.NewReader(strings.NewReader(head))
	req, perr := http.ReadRequest(br)
	if perr != nil {
		return "", "", nil, nil, fmt.Errorf("解析录制请求头失败: %v", perr)
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if req.Host != "" {
		h.Set("Host", req.Host)
	}
	return strings.ToUpper(method), rawURL, h, reqBody, nil
}

// ReplayTool 构造 http_replay 工具（由 Traffic.Tools() 汇入 agent 工具集）。
// 走平台出口：捕获开时上游是记录代理，重放流量自动留痕；关闭时走全局代理/
// 直连。.onion/.i2p 按 host 分流（与 http_request 口径一致）。
func (t *Traffic) ReplayTool() actool.CoreTool {
	allow := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	return actool.Build(actool.Spec{
		Name: "http_replay",
		Description: "从已录制流量里取一条请求并重放，可覆盖 method/url/headers/body——authz 测试的原子操作。" +
			"先用 traffic_search 找 traffic_id，典型用法：原样重放看基线，再只换 session cookie/Authorization 头重放看越权回读差异。" +
			"走平台出口，捕获开启时重放本身也被录制留痕；正文是截断 blob 的记录不可重放。",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"traffic_id": strSchema("traffic_search 返回的真实流量 ID"),
				"method":     strSchema("覆盖请求方法（可选；默认沿用录制请求）"),
				"url":        strSchema("覆盖完整 URL（可选；默认沿用录制 URL）"),
				"headers":    strSchema("覆盖请求头，每行一条 'Key: Value'（可选；只覆盖给出的键，其余沿用录制头）"),
				"body":       strSchema("覆盖请求体原文（可选；默认沿用录制正文）"),
				"timeout":    map[string]any{"type": "integer", "description": "超时秒数，默认 30，最大 120"},
			},
			"required": []any{"traffic_id"},
		},
		ReadOnly:    func(json.RawMessage) bool { return false }, // 会向目标发请求
		Permissions: allow,
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			var a struct {
				TrafficID string `json:"traffic_id"`
				Method    string `json:"method"`
				URL       string `json:"url"`
				Headers   string `json:"headers"`
				Body      string `json:"body"`
				Timeout   int    `json:"timeout"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.TrafficID) == "" {
				return actool.Errorf("traffic_id 必填（先 traffic_search）"), nil
			}
			method, rawURL, header, body, err := t.replayRequest(a.TrafficID)
			if err != nil {
				return actool.Errorf("读取录制请求失败: " + err.Error()), nil
			}
			if strings.TrimSpace(a.Method) != "" {
				method = strings.ToUpper(strings.TrimSpace(a.Method))
			}
			if strings.TrimSpace(a.URL) != "" {
				rawURL = strings.TrimSpace(a.URL)
			}
			if rawURL == "" {
				return actool.Errorf("录制条目没有 URL，请用 url 参数显式指定"), nil
			}
			u, err := url.Parse(rawURL)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return actool.Errorf("url 必须是含 scheme 的绝对 URL（如 http://target/api）"), nil
			}
			for _, line := range strings.Split(a.Headers, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				k, v, ok := strings.Cut(line, ":")
				if !ok || strings.TrimSpace(k) == "" {
					return actool.Errorf("请求头格式错误（应为 Key: Value）: " + line), nil
				}
				header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
			}
			reqBody := body
			if a.Body != "" {
				reqBody = []byte(a.Body)
			}
			var rdr io.Reader
			if len(reqBody) > 0 {
				rdr = bytes.NewReader(reqBody)
			}
			req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
			if err != nil {
				return actool.Errorf("构造请求失败: " + err.Error()), nil
			}
			req.Header = header
			// 出口：与录制同链路。pickUpstream 按 .onion/.i2p → 专属代理 → 代理池
			// → 全局上游分流；未配置代理时直连。
			tr := &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // 授权测试：自签证书常态
				TLSHandshakeTimeout: 15 * time.Second,
			}
			if upstream, perr := t.pickUpstream(req); perr == nil && upstream != nil {
				if perr := proxyprobe.ConfigureTransport(tr, upstream.String(), proxyprobe.HiddenProxies{}); perr != nil {
					return actool.Errorf("出口代理解析失败: " + perr.Error()), nil
				}
			}
			timeout := a.Timeout
			if timeout <= 0 {
				timeout = 30
			}
			if timeout > 120 {
				timeout = 120
			}
			client := &http.Client{Transport: tr, Timeout: time.Duration(timeout) * time.Second}
			start := time.Now()
			resp, derr := client.Do(req)
			if derr != nil {
				return actool.Errorf(fmt.Sprintf("重放失败：%v（先查出口代理是否可达，不要直接判目标封板）", derr)), nil
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 200_000))
			ms := time.Since(start).Milliseconds()
			head := make(map[string]string, len(resp.Header))
			for k, v := range resp.Header {
				head[k] = strings.Join(v, ", ")
			}
			out := map[string]any{
				"status":      resp.StatusCode,
				"status_text": resp.Status,
				"url":         resp.Request.URL.String(),
				"method":      method,
				"headers":     head,
				"body":        string(raw),
				"bytes":       len(raw),
				"millis":      ms,
				"truncated":   len(raw) >= 200_000,
			}
			b, _ := json.Marshal(out)
			return actool.Text(string(b)), nil
		},
	})
}

func strSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
