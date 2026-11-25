package traffic

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// N3 http_replay 回归：工具注册进 traffic.Tools()（从而经 host 注入三 agent），
// 录制请求的解析（method/url/headers/body）与覆盖口径，截断正文拒绝重放。
func TestReplayToolRegisteredAndParses(t *testing.T) {
	tDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer tDB.Close()
	for _, ddl := range []string{indexSchema, ftsSchema} {
		if _, err := tDB.Exec(ddl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("schema: %v", err)
		}
	}
	tr := &Traffic{db: tDB}
	// 一条录制请求：GET /api/admin 带 Cookie，正文为空。
	if _, err := tDB.Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('r1',1,'target.example','GET','/api/admin','http://target.example/api/admin',200,'',0,0,'/api/admin')`); err != nil {
		t.Fatal(err)
	}
	head := "GET /api/admin HTTP/1.1\r\nHost: target.example\r\nCookie: session=old\r\nUser-Agent: test\r\n\r\n"
	if _, err := tDB.Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,resp_head,resp_body) VALUES('r1',?,?,?,?)`, head, []byte{}, "", []byte{}); err != nil {
		t.Fatal(err)
	}
	method, rawURL, hdr, body, err := tr.replayRequest("r1")
	if err != nil {
		t.Fatalf("replayRequest: %v", err)
	}
	if method != "GET" || rawURL != "http://target.example/api/admin" {
		t.Fatalf("parsed method/url = %q %q", method, rawURL)
	}
	if hdr.Get("Cookie") != "session=old" || hdr.Get("Host") != "target.example" || len(body) != 0 {
		t.Fatalf("parsed headers/body = %v %q", hdr, body)
	}

	// 工具注册（tools 表 seed 与 host 注入都走 Tools()）。
	var found bool
	for _, tool := range tr.Tools() {
		if tool.Name() == "http_replay" {
			found = true
		}
	}
	if !found {
		t.Fatal("http_replay 未注册进 Traffic.Tools()")
	}
	// 参数校验：缺 traffic_id 时直接报错，不打目标。
	tool := tr.ReplayTool()
	r, err := tool.Call(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil || !r.IsError || !strings.Contains(r.Flatten(), "traffic_id 必填") {
		t.Fatalf("missing traffic_id: res=%v err=%v", r, err)
	}
}
