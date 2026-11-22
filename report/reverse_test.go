package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

func reverseFixture() ([]*db.ReverseSample, map[int64][]db.ReverseSecret) {
	meta := map[string]any{
		"format":    "PE",
		"machine":   "AMD64",
		"subsystem": "Windows GUI",
		"magic":     "PE32+",
		"dotnet":    true,
		"signed":    false,
		"sections": []map[string]any{
			{"name": ".text", "size": 4096, "entropy": 6.12, "executable": true},
			{"name": ".rdata", "size": 2048, "entropy": 4.4},
		},
		"imports": []map[string]any{
			{"dll": "KERNEL32.dll", "funcs": []string{"CreateFileW", "ReadFile"}},
		},
		"urls":        []string{"https://api.example.com/v1"},
		"strings_top": []string{"password = S3cr3tP4ssw0rd"},
		"note":        "检测到 .NET/CLR；可接入 ILSpy 反编译（后续批次）。",
	}
	raw, _ := json.Marshal(meta)
	sample := &db.ReverseSample{
		ID: 1, TaskID: 9, Filename: "client.exe", SHA256: "abcdef123456",
		SizeBytes: 1024 * 1024, Kind: "pe", Status: "analyzed",
		Metadata: raw, SecretCount: 2,
		DecompileEngine: "ilspy", DecompileStatus: "done",
	}
	secrets := map[int64][]db.ReverseSecret{
		1: {
			{
				SampleID: 1, Rule: "aws_access_key", Match: "AKIA1234567890ABCDEF",
				Context: "aws_access_key_id = AKIA1234567890ABCDEF", Section: ".rdata",
				Offset: 16, Confidence: 0.95,
			},
			{
				SampleID: 1, Rule: "generic_secret", Match: "DecompiledToken1",
				Context: "var token = \"DecompiledToken1\"", Section: "Program.cs",
				Source: "decompile", Offset: 40, Confidence: 0.5,
			},
		},
	}
	return []*db.ReverseSample{sample}, secrets
}

func TestReverseChapterMarkdown(t *testing.T) {
	samples, secrets := reverseFixture()
	md := TaskMarkdown(TaskDocxInput{
		Title: "渗透测试报告 — 逆向测试", GeneratedAt: time.Now(),
		ReverseSamples: samples, ReverseSecrets: secrets,
	})
	for _, want := range []string{
		"## 五、客户端/二进制分析",
		"client.exe",
		"Windows PE",
		".NET/CLR",
		".text",
		"AWS Access Key",
		"AKIA1234567890ABCDEF",
		"https://api.example.com/v1",
		"**反编译**：ilspy · 已完成",
		"反编译",
		"## 六、修复优先级路线图",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q\n%s", want, md)
		}
	}
}

func TestReverseChapterEmpty(t *testing.T) {
	md := TaskMarkdown(TaskDocxInput{Title: "t", GeneratedAt: time.Now()})
	if !strings.Contains(md, "## 五、客户端/二进制分析") || !strings.Contains(md, "未上传客户端/二进制样本") {
		t.Fatalf("empty reverse chapter missing explanation:\n%s", md)
	}
}

func TestBuildTaskDocxWithReverse(t *testing.T) {
	samples, secrets := reverseFixture()
	doc := BuildTaskDocx(TaskDocxInput{
		Title: "渗透测试报告 — 逆向测试", GeneratedAt: time.Now(),
		ReverseSamples: samples, ReverseSecrets: secrets,
	})
	var buf bytes.Buffer
	if err := doc.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty docx")
	}
}
