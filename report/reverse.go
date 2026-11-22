package report

// 客户端/二进制分析章节（P0）：静态 triage + 敏感信息候选。报告与任务详情
// 读同一份 reverse_samples/reverse_secrets 数据；明确声明未执行样本。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/reverse"
)

func reverseKindCN(kind string) string {
	switch kind {
	case "pe":
		return "Windows PE"
	case "pe-invalid":
		return "PE（解析失败）"
	case "elf":
		return "ELF"
	case "apk":
		return "Android APK"
	case "jar":
		return "Java JAR"
	case "zip":
		return "ZIP"
	case "electron-asar":
		return "Electron ASAR"
	default:
		return nz(kind, "二进制")
	}
}

func reverseRuleCN(rule string) string {
	switch rule {
	case "private_key":
		return "私钥"
	case "aws_access_key":
		return "AWS Access Key"
	case "aws_secret_key":
		return "AWS Secret Key"
	case "github_token":
		return "GitHub Token"
	case "google_api_key":
		return "Google API Key"
	case "slack_token":
		return "Slack Token"
	case "stripe_key":
		return "Stripe Key"
	case "aliyun_access_key":
		return "阿里云 AccessKey"
	case "tencent_secret_id":
		return "腾讯云 SecretId"
	case "jwt":
		return "JWT"
	case "bearer_token":
		return "Bearer Token"
	case "basic_auth":
		return "Basic Auth"
	case "database_url":
		return "数据库连接串"
	case "jdbc_url":
		return "JDBC 连接串"
	case "internal_url":
		return "内网地址"
	case "generic_secret":
		return "通用凭据"
	default:
		return rule
	}
}

func reverseSecretSourceCN(source string) string {
	if source == "decompile" {
		return "反编译"
	}
	return "二进制"
}

func reverseDecompileStatusCN(status string) string {
	switch status {
	case "running":
		return "进行中"
	case "done":
		return "已完成"
	case "failed":
		return "失败"
	default:
		return nz(status, "-")
	}
}

func reverseMetaOf(s *db.ReverseSample) reverse.Meta {
	var m reverse.Meta
	if len(s.Metadata) > 0 {
		_ = json.Unmarshal(s.Metadata, &m)
	}
	return m
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func writeReverseChapterDocx(d *Docx, samples []*db.ReverseSample, secrets map[int64][]db.ReverseSecret) {
	if len(samples) == 0 {
		d.P("本次任务未上传客户端/二进制样本。该章在用户上传样本后自动生成；" +
			"所有分析均为静态读取，平台不执行样本。")
		return
	}
	d.P("本章对用户上传的客户端/二进制样本做静态 triage 与敏感信息扫描，全程不执行样本。" +
		"规则命中是线索，需人工或用真实接口验证后才能定性为漏洞。")

	for i, s := range samples {
		meta := reverseMetaOf(s)
		d.H2(fmt.Sprintf("5.%d %s", i+1, nz(s.Filename, "样本")))
		d.P(fmt.Sprintf("SHA-256：%s", nz(s.SHA256, "-")))
		d.P(fmt.Sprintf("类型：%s · 大小：%s · 状态：%s", reverseKindCN(s.Kind), humanSize(s.SizeBytes), reverseStatusCN(s.Status)))
		if s.Error != "" {
			d.P("分析错误：" + s.Error)
		}
		if meta.Format != "" {
			d.P(fmt.Sprintf("格式：%s · 架构：%s · 子系统：%s", meta.Format, nz(meta.Machine, "-"), nz(meta.Subsystem, "-")))
		}
		var tags []string
		if meta.DotNet {
			tags = append(tags, ".NET/CLR")
		}
		if meta.Signed {
			tags = append(tags, "含 Authenticode 签名（未验证）")
		}
		if meta.Packer != "" {
			tags = append(tags, "壳："+meta.Packer)
		}
		if meta.PDB != "" {
			tags = append(tags, "PDB："+meta.PDB)
		}
		if len(meta.Mitigations) > 0 {
			tags = append(tags, "缓解："+strings.Join(meta.Mitigations, " / "))
		}
		if len(tags) > 0 {
			for _, t := range tags {
				d.Bullet(t)
			}
		}
		if s.DecompileStatus != "" {
			d.Bullet(fmt.Sprintf("反编译：%s · %s", nz(s.DecompileEngine, "-"), reverseDecompileStatusCN(s.DecompileStatus)))
		}
		if s.DecompileError != "" {
			d.P("反编译错误：" + s.DecompileError)
		}
		if meta.Note != "" {
			d.P(meta.Note)
		}

		if len(meta.Sections) > 0 {
			d.H4("节区")
			rows := make([][]string, 0, len(meta.Sections))
			for _, sec := range meta.Sections {
				var flags []string
				if sec.Executable {
					flags = append(flags, "X")
				}
				if sec.Writable {
					flags = append(flags, "W")
				}
				rows = append(rows, []string{sec.Name, humanSize(int64(sec.Size)), fmt.Sprintf("%.2f", sec.Entropy), strings.Join(flags, "")})
			}
			d.Table([]string{"节区", "大小", "熵", "属性"}, rows)
		}
		if len(meta.Imports) > 0 {
			d.H4("导入表（节选）")
			for i, g := range meta.Imports {
				if i >= 12 {
					break
				}
				d.Bullet(fmt.Sprintf("%s：%s", g.DLL, strings.Join(g.Funcs, ", ")))
			}
		}
		if len(meta.URLs) > 0 {
			d.H4("字符串中的 URL（节选）")
			for i, u := range meta.URLs {
				if i >= 20 {
					break
				}
				d.Bullet(u)
			}
		}
		if len(meta.StringsTop) > 0 {
			d.H4("值得关注的字符串（节选）")
			for i, str := range meta.StringsTop {
				if i >= 15 {
					break
				}
				d.Bullet(chainTruncateText(str, 200))
			}
		}

		list := secrets[s.ID]
		d.H4("敏感信息候选")
		if len(list) == 0 {
			d.P("未命中规则库中的凭据形态；仍建议结合反编译人工复核。")
			continue
		}
		rows := make([][]string, 0, len(list))
		for _, x := range list {
			loc := x.Section
			if loc == "" {
				loc = "-"
			}
			rows = append(rows, []string{
				reverseRuleCN(x.Rule),
				chainTruncateText(x.Match, 120),
				fmt.Sprintf("%s @ 0x%x", loc, x.Offset),
				reverseSecretSourceCN(x.Source),
				fmt.Sprintf("%.0f%%", x.Confidence*100),
			})
		}
		d.Table([]string{"规则", "命中", "位置", "来源", "置信度"}, rows)
		d.P("命中上下文（节选）：")
		for i, x := range list {
			if i >= 10 {
				break
			}
			d.Bullet(fmt.Sprintf("[%s] %s", reverseRuleCN(x.Rule), chainTruncateText(x.Context, 200)))
		}
	}
}

func reverseStatusCN(status string) string {
	switch status {
	case "analyzed":
		return "已分析"
	case "failed":
		return "分析失败"
	case "uploaded":
		return "待分析"
	default:
		return nz(status, "-")
	}
}

func writeReverseChapterMarkdown(b *strings.Builder, samples []*db.ReverseSample, secrets map[int64][]db.ReverseSecret) {
	b.WriteString("## 五、客户端/二进制分析\n\n")
	if len(samples) == 0 {
		b.WriteString("_本次任务未上传客户端/二进制样本。所有分析均为静态读取，平台不执行样本。_\n\n")
		return
	}
	b.WriteString("本章对用户上传的客户端/二进制样本做静态 triage 与敏感信息扫描，全程不执行样本。规则命中是线索，需人工或用真实接口验证后才能定性为漏洞。\n\n")
	for i, s := range samples {
		meta := reverseMetaOf(s)
		fmt.Fprintf(b, "### 5.%d %s\n\n", i+1, nz(s.Filename, "样本"))
		fmt.Fprintf(b, "- **SHA-256**：`%s`\n", nz(s.SHA256, "-"))
		fmt.Fprintf(b, "- **类型**：%s · **大小**：%s · **状态**：%s\n", reverseKindCN(s.Kind), humanSize(s.SizeBytes), reverseStatusCN(s.Status))
		if meta.Format != "" {
			fmt.Fprintf(b, "- **格式**：%s · **架构**：%s · **子系统**：%s\n", meta.Format, nz(meta.Machine, "-"), nz(meta.Subsystem, "-"))
		}
		if meta.DotNet {
			b.WriteString("- **.NET/CLR**：是\n")
		}
		if meta.Signed {
			b.WriteString("- **Authenticode 签名**：存在（未验证）\n")
		}
		if meta.Packer != "" {
			fmt.Fprintf(b, "- **壳**：%s\n", meta.Packer)
		}
		if meta.PDB != "" {
			fmt.Fprintf(b, "- **PDB**：`%s`\n", meta.PDB)
		}
		if len(meta.Mitigations) > 0 {
			fmt.Fprintf(b, "- **缓解**：%s\n", strings.Join(meta.Mitigations, " / "))
		}
		if s.Error != "" {
			fmt.Fprintf(b, "- **分析错误**：%s\n", s.Error)
		}
		if meta.Note != "" {
			fmt.Fprintf(b, "\n%s\n", meta.Note)
		}
		if s.DecompileStatus != "" {
			fmt.Fprintf(b, "- **反编译**：%s · %s\n", nz(s.DecompileEngine, "-"), reverseDecompileStatusCN(s.DecompileStatus))
		}
		if s.DecompileError != "" {
			fmt.Fprintf(b, "- **反编译错误**：%s\n", s.DecompileError)
		}
		b.WriteString("\n")

		if len(meta.Sections) > 0 {
			b.WriteString("**节区**\n\n| 节区 | 大小 | 熵 | 属性 |\n|---|---|---|---|\n")
			for _, sec := range meta.Sections {
				var flags []string
				if sec.Executable {
					flags = append(flags, "X")
				}
				if sec.Writable {
					flags = append(flags, "W")
				}
				fmt.Fprintf(b, "| %s | %s | %.2f | %s |\n", mdCell(sec.Name), humanSize(int64(sec.Size)), sec.Entropy, strings.Join(flags, ""))
			}
			b.WriteString("\n")
		}
		if len(meta.Imports) > 0 {
			b.WriteString("**导入表（节选）**\n\n")
			for i, g := range meta.Imports {
				if i >= 12 {
					break
				}
				fmt.Fprintf(b, "- %s：%s\n", g.DLL, strings.Join(g.Funcs, ", "))
			}
			b.WriteString("\n")
		}
		if len(meta.URLs) > 0 {
			b.WriteString("**字符串中的 URL（节选）**\n\n")
			for i, u := range meta.URLs {
				if i >= 20 {
					break
				}
				fmt.Fprintf(b, "- `%s`\n", u)
			}
			b.WriteString("\n")
		}
		if len(meta.StringsTop) > 0 {
			b.WriteString("**值得关注的字符串（节选）**\n\n")
			for i, str := range meta.StringsTop {
				if i >= 15 {
					break
				}
				fmt.Fprintf(b, "- `%s`\n", chainTruncateText(str, 200))
			}
			b.WriteString("\n")
		}

		list := secrets[s.ID]
		b.WriteString("**敏感信息候选**\n\n")
		if len(list) == 0 {
			b.WriteString("_未命中规则库中的凭据形态；仍建议结合反编译人工复核。_\n\n")
			continue
		}
		b.WriteString("| 规则 | 命中 | 位置 | 来源 | 置信度 |\n|---|---|---|---|---|\n")
		for _, x := range list {
			loc := x.Section
			if loc == "" {
				loc = "-"
			}
			fmt.Fprintf(b, "| %s | `%s` | %s @ 0x%x | %s | %.0f%% |\n",
				reverseRuleCN(x.Rule), mdCell(chainTruncateText(x.Match, 120)), loc, x.Offset,
				reverseSecretSourceCN(x.Source), x.Confidence*100)
		}
		b.WriteString("\n**命中上下文（节选）**\n\n")
		for i, x := range list {
			if i >= 10 {
				break
			}
			fmt.Fprintf(b, "- [%s] %s\n", reverseRuleCN(x.Rule), chainTruncateText(x.Context, 200))
		}
		b.WriteString("\n")
	}
}
