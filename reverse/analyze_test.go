package reverse

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		if r > 0x7f {
			r = '?'
		}
		out = append(out, byte(r), 0)
	}
	return out
}

// 秘密扫描：ASCII / UTF-16LE 都要命中，且带偏移与上下文。
func TestAnalyzeSecrets(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("config header\n")
	buf.WriteString("aws_access_key_id = AKIA1234567890ABCDEF\n")
	buf.WriteString("password = S3cr3tP4ssw0rd\n")
	buf.WriteString("db = mysql://root:Passw0rd@10.1.2.3:3306/app\n")
	buf.WriteString("-----BEGIN RSA PRIVATE KEY-----\n")
	buf.WriteString("url = https://internal.example.com/api/v1\n")
	buf.Write(utf16le("token = WideSecret12345"))
	p := writeTemp(t, "sample.bin", buf.Bytes())

	res, err := Analyze(p, "sample.bin")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range res.Secrets {
		got[s.Rule] = true
		if s.Offset <= 0 {
			t.Fatalf("secret %s missing offset", s.Rule)
		}
		if strings.TrimSpace(s.Context) == "" {
			t.Fatalf("secret %s missing context", s.Rule)
		}
	}
	for _, want := range []string{"aws_access_key", "private_key", "database_url", "generic_secret"} {
		if !got[want] {
			t.Fatalf("missing rule %s; got %v", want, got)
		}
	}
	// UTF-16LE 的 token 也应被 generic_secret 命中。
	foundWide := false
	for _, s := range res.Secrets {
		if strings.Contains(s.Match, "WideSecret12345") {
			foundWide = true
			break
		}
	}
	if !foundWide {
		t.Fatalf("UTF-16LE secret not found: %+v", res.Secrets)
	}
	if res.Meta.SecretCount != len(res.Secrets) || res.Meta.StringCount == 0 {
		t.Fatalf("meta counts wrong: %+v", res.Meta)
	}
}

func TestScanText(t *testing.T) {
	text := `var aws = "AKIA1234567890ABCDEF"; var password = "S3cr3tP4ssw0rd";`
	hits := ScanText(text)
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	rules := map[string]bool{}
	for _, h := range hits {
		rules[h.Rule] = true
	}
	if !rules["aws_access_key"] || !rules["generic_secret"] {
		t.Fatalf("hits=%+v", hits)
	}
}

// ZIP 内的 APK 识别。
func TestAnalyzeAPKZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"AndroidManifest.xml", "classes.dex", "res/values/strings.xml"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("dummy"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := writeTemp(t, "app.apk", buf.Bytes())
	res, err := Analyze(p, "app.apk")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "apk" {
		t.Fatalf("kind=%q want apk; meta=%+v", res.Kind, res.Meta)
	}
	if res.Meta.Format != "Android APK" {
		t.Fatalf("format=%q", res.Meta.Format)
	}
}

// MZ 头但 PE 解析失败时降级为 pe-invalid，而不是报错。
func TestAnalyzeInvalidPE(t *testing.T) {
	data := append([]byte("MZ"), bytes.Repeat([]byte{0}, 256)...)
	p := writeTemp(t, "broken.exe", data)
	res, err := Analyze(p, "broken.exe")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "pe-invalid" {
		t.Fatalf("kind=%q want pe-invalid", res.Kind)
	}
}

// 平台自带的真实二进制：至少能正确识别格式并解析节区。
func TestAnalyzeRealBinary(t *testing.T) {
	var candidates []string
	if runtime.GOOS == "windows" {
		if wd := os.Getenv("WINDIR"); wd != "" {
			candidates = append(candidates, filepath.Join(wd, "System32", "notepad.exe"))
		}
	} else {
		candidates = append(candidates, "/bin/ls", "/usr/bin/ls")
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		res, err := Analyze(p, filepath.Base(p))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			if res.Kind != "pe" {
				t.Fatalf("windows binary kind=%q want pe", res.Kind)
			}
			if len(res.Meta.Sections) == 0 {
				t.Fatal("PE sections empty")
			}
			if res.Meta.Machine == "" || res.Meta.Magic == "" {
				t.Fatalf("PE header incomplete: %+v", res.Meta)
			}
		} else {
			if res.Kind != "elf" {
				t.Fatalf("elf binary kind=%q want elf", res.Kind)
			}
		}
		return
	}
	t.Skip("no platform binary available")
}
