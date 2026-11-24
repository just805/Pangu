package agent

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mpTestFile struct {
	name string
	body string
}

// buildWxapkg 合成一个标准 wxapkg（v1）用于验证解包器：key=0 为明文，非 0 时
// 对文件名与数据做单字节 XOR（头、fileCount、offset/size 保持明文）。
func buildWxapkg(t *testing.T, key byte, files []mpTestFile) []byte {
	t.Helper()
	names := make([][]byte, len(files))
	entriesLen := 0
	for i, f := range files {
		nb := []byte(f.name)
		if key != 0 {
			for j := range nb {
				nb[j] ^= key
			}
		}
		names[i] = nb
		entriesLen += 4 + len(nb) + 4 + 4
	}
	indexInfoLength := 4 + entriesLen
	dataStart := 14 + indexInfoLength
	offsets := make([]int, len(files))
	off := dataStart
	for i, f := range files {
		offsets[i] = off
		off += len(f.body)
	}
	var body []byte
	for _, f := range files {
		b := []byte(f.body)
		if key != 0 {
			for j := range b {
				b[j] ^= key
			}
		}
		body = append(body, b...)
	}

	hdr := make([]byte, 18)
	hdr[0] = 0xBE
	binary.BigEndian.PutUint32(hdr[1:], 0)
	binary.BigEndian.PutUint32(hdr[5:], uint32(indexInfoLength))
	binary.BigEndian.PutUint32(hdr[9:], uint32(len(body)))
	hdr[13] = 0xED
	binary.BigEndian.PutUint32(hdr[14:], uint32(len(files)))

	buf := append([]byte{}, hdr...)
	var tmp [4]byte
	for i, nb := range names {
		binary.BigEndian.PutUint32(tmp[:], uint32(len(nb)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, nb...)
		binary.BigEndian.PutUint32(tmp[:], uint32(offsets[i]))
		buf = append(buf, tmp[:]...)
		binary.BigEndian.PutUint32(tmp[:], uint32(len(files[i].body)))
		buf = append(buf, tmp[:]...)
	}
	buf = append(buf, body...)
	return buf
}

func TestMiniprogramUnpackPlainAndXOR(t *testing.T) {
	files := []mpTestFile{
		{"/app-config.json", `{"pages":["pages/index/index"]}`},
		{"/pages/index/index.js", `console.log("mini");`},
	}
	for _, key := range []byte{0, 0x5A} {
		dir := t.TempDir()
		in := filepath.Join(dir, "test.wxapkg")
		if err := os.WriteFile(in, buildWxapkg(t, key, files), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "out")
		res, err := unpackWxapkgFile(in, out)
		if err != nil {
			t.Fatalf("key=%#x unpack: %v", key, err)
		}
		if res.FileCount != 2 {
			t.Fatalf("key=%#x file count = %d, want 2", key, res.FileCount)
		}
		if !res.AppConfig {
			t.Errorf("key=%#x app-config.json not detected", key)
		}
		got, err := os.ReadFile(filepath.Join(out, "app-config.json"))
		if err != nil {
			t.Fatalf("key=%#x read app-config.json: %v", key, err)
		}
		if string(got) != files[0].body {
			t.Errorf("key=%#x app-config content = %q, want %q", key, got, files[0].body)
		}
		js, err := os.ReadFile(filepath.Join(out, "pages", "index", "index.js"))
		if err != nil {
			t.Fatalf("key=%#x read index.js: %v", key, err)
		}
		if string(js) != files[1].body {
			t.Errorf("key=%#x index.js content = %q, want %q", key, js, files[1].body)
		}
	}
}

func TestMiniprogramUnpackRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "evil.wxapkg")
	files := []mpTestFile{{"/../../evil.txt", "pwn"}}
	if err := os.WriteFile(in, buildWxapkg(t, 0, files), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unpackWxapkgFile(in, filepath.Join(dir, "out")); err == nil {
		t.Fatal("path traversal package must be rejected")
	}
}

func TestMiniprogramAudit(t *testing.T) {
	dir := t.TempDir()
	src := `const appid = "wx1234567890abcdef";
const s = "AppSecret: abcdef1234567890";
wx.cloud.init({ env: "prod-abc123" });
wx.cloud.callFunction({ name: "login" });
wx.request({ url: "https://api.example.com/user/info" });
//# sourceMappingURL=app.js.map
function f(openid) { return openid; }
`
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := auditMiniProgram(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	hasEndpoint := false
	for _, e := range res.Endpoints {
		if e.URL == "https://api.example.com/user/info" {
			hasEndpoint = true
		}
	}
	if !hasEndpoint {
		t.Errorf("endpoint not found: %+v", res.Endpoints)
	}
	if len(res.Secrets) == 0 {
		t.Error("hardcoded secret not detected")
	}
	if len(res.Cloud.Envs) == 0 || res.Cloud.Envs[0] != "prod-abc123" {
		t.Errorf("cloud env = %v, want prod-abc123", res.Cloud.Envs)
	}
	if len(res.Cloud.Functions) == 0 || res.Cloud.Functions[0] != "login" {
		t.Errorf("cloud functions = %v, want login", res.Cloud.Functions)
	}
	if len(res.AppIDs) == 0 || res.AppIDs[0] != "wx1234567890abcdef" {
		t.Errorf("appid = %v", res.AppIDs)
	}
	if len(res.SourceMaps) == 0 {
		t.Error("sourcemap not detected")
	}
	if !containsString(res.SensitiveParams, "openid") {
		t.Errorf("sensitive params = %v, want openid", res.SensitiveParams)
	}
	if len(res.Hints) == 0 {
		t.Error("expected non-empty hints")
	}
	if strings.Contains(res.Secrets[0].Preview, "abcdef1234567890") {
		t.Errorf("secret preview must be redacted, got %q", res.Secrets[0].Preview)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
