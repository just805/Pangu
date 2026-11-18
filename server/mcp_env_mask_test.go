package server

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

// GET /api/mcp 此前把 env（含 Authorization/GITHUB_TOKEN 等凭据）原样回显，
// 任何已登录会话都能读到明文。掩码后：键名保留、值统一替换成占位符；保存时
// 占位符从库中原值还原（restoreMaskedMCPEnv），未还原则一次普通编辑就会把
// 凭据覆盖成掩码文本。
func TestMaskMCPEnv(t *testing.T) {
	raw := json.RawMessage(`{"Authorization":"Bearer secret","GITHUB_TOKEN":"ghp_x","PLAIN":"no-secret"}`)
	got := maskMCPEnv(raw)
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("maskMCPEnv 输出不是合法 JSON: %v", err)
	}
	if len(m) != 3 {
		t.Fatalf("键数量改变: %v", m)
	}
	for k, v := range m {
		if v != mcpEnvMask {
			t.Fatalf("键 %s 的值未被掩码: %q", k, v)
		}
	}
	if strings.Contains(string(got), "Bearer secret") || strings.Contains(string(got), "ghp_x") {
		t.Fatalf("掩码输出泄露原值: %s", got)
	}

	// 空 env / 异常 JSON 原样返回。
	if got := maskMCPEnv(json.RawMessage(`{}`)); string(got) != `{}` {
		t.Fatalf("空 env 应原样返回, got %s", got)
	}
	if got := maskMCPEnv(json.RawMessage(`null`)); string(got) != `null` {
		t.Fatalf("null 应原样返回, got %s", got)
	}
	if got := maskMCPEnv(json.RawMessage(`not-json`)); string(got) != `not-json` {
		t.Fatalf("非法 JSON 应原样返回, got %s", got)
	}
}

// 端到端：GET /api/mcp 掩码 → 前端把掩码值原样传回 POST /api/mcp → 库中原值
// 保留；改成新值则按新值落库。需要真 PG（与全量测试同 fixture 模式）。
func TestMCPEndpointMasksEnvAndPreservesOnSave(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	pg := m.pg
	pg.Exec(`DELETE FROM mcp_servers WHERE name='t-mask'`)

	id, err := pg.SaveMCP(&db.MCPServer{
		Name: "t-mask", Transport: "http", URL: "https://example.invalid/mcp",
		Env: json.RawMessage(`{"Authorization":"Bearer real-secret"}`), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(`DELETE FROM mcp_servers WHERE name='t-mask'`)

	s := &Server{m: m}
	// 1) GET /api/mcp：值必须被掩码，原值不得出现。
	w := httptest.NewRecorder()
	s.pgListMCP(w, httptest.NewRequest("GET", "/api/mcp", nil))
	if w.Code != 200 {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "real-secret") {
		t.Fatalf("GET /api/mcp 泄露明文凭据: %s", w.Body.String())
	}
	var listed struct {
		Servers []db.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var found *db.MCPServer
	for i := range listed.Servers {
		if listed.Servers[i].ID == id {
			found = &listed.Servers[i]
		}
	}
	if found == nil {
		t.Fatalf("列表里没有刚建的 server: %s", w.Body.String())
	}
	if string(found.Env) != `{"Authorization":"__PANGU_MASKED__"}` {
		t.Fatalf("env 未按预期掩码: %s", found.Env)
	}

	// 2) 保存（掩码值原样传回）→ 原值保留。
	body := `{"id":` + strconv.FormatInt(id, 10) + `,"name":"t-mask","transport":"http","url":"https://example.invalid/mcp","env":{"Authorization":"__PANGU_MASKED__"},"enabled":true}`
	w2 := httptest.NewRecorder()
	s.pgSaveMCP(w2, httptest.NewRequest("POST", "/api/mcp", strings.NewReader(body)))
	if w2.Code != 200 {
		t.Fatalf("save(masked) status=%d body=%s", w2.Code, w2.Body.String())
	}
	raw, err := pg.MCPEnvByID(id)
	if err != nil {
		t.Fatal(err)
	}
	// jsonb 列回读的文本会被 PG 规范化（键后带空格），不能做字符串等值断言。
	var storedEnv map[string]string
	if err := json.Unmarshal(raw, &storedEnv); err != nil {
		t.Fatal(err)
	}
	if storedEnv["Authorization"] != "Bearer real-secret" {
		t.Fatalf("掩码还原失败，原值被覆盖: %s", raw)
	}

	// 3) 保存（用户改成新值）→ 新值落库。
	body = `{"id":` + strconv.FormatInt(id, 10) + `,"name":"t-mask","transport":"http","url":"https://example.invalid/mcp","env":{"Authorization":"Bearer rotated-secret"},"enabled":true}`
	w3 := httptest.NewRecorder()
	s.pgSaveMCP(w3, httptest.NewRequest("POST", "/api/mcp", strings.NewReader(body)))
	if w3.Code != 200 {
		t.Fatalf("save(new) status=%d body=%s", w3.Code, w3.Body.String())
	}
	raw, err = pg.MCPEnvByID(id)
	if err != nil {
		t.Fatal(err)
	}
	var newEnv map[string]string
	if err := json.Unmarshal(raw, &newEnv); err != nil {
		t.Fatal(err)
	}
	if newEnv["Authorization"] != "Bearer rotated-secret" {
		t.Fatalf("新值未落库: %s", raw)
	}
}
