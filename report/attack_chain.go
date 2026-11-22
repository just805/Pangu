package report

// 攻击路径（路线图 C2/C3）：把 db.AttackChain 渲染成 Word 章节 + Markdown 章节，
// 并把链路画成 PNG 内嵌进 Word。图形用手写 PNG（标准库 + 内嵌仿宋字体），
// 不引入 graphviz/cgo 依赖——部署环境无需额外系统库。

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	"github.com/golang/freetype/truetype"
	"github.com/just805/pangu/db"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

func chainRelCN(rel string) string {
	switch rel {
	case "derived_from":
		return "显式前置"
	case "enables":
		return "推进"
	case "chained_with":
		return "同源并报"
	}
	return rel
}

// chainPhaseCN 给首/末步骤标出业务阶段，帮助读者把技术链路映射到攻击叙事。
func chainPhaseCN(seq, total int) string {
	if total < 2 {
		return ""
	}
	if seq == 1 {
		return "（初始访问）"
	}
	if seq == total {
		return "（最终影响）"
	}
	return ""
}

// writeAttackChainDocx 渲染「四、攻击路径」章：链路总览表 + 逐步叙事 + 证明点 +
// 关系说明 + 路径图。没有链时明确说明原因，不静默留白。
func writeAttackChainDocx(d *Docx, c *db.AttackChain, names map[int64]string) {
	if c == nil || len(c.Steps) == 0 {
		d.P("本次任务未形成可识别的攻击链：发现尚未绑定产出意图，或链路不足两步。" +
			"漏洞明细仍按类别完整呈现，不影响交付结论。")
		return
	}
	d.P(fmt.Sprintf("本章按探索图血缘还原从初始访问到最终影响的推进路径，共 %d 步、%d 条推进关系。"+
		"每一步都附可核验的证明点（事实/流量证据/原始证据），未证明的推断不进入链路。",
		len(c.Steps), len(c.Edges)))

	d.H2("链路总览")
	rows := make([][]string, 0, len(c.Steps))
	for _, s := range c.Steps {
		rows = append(rows, []string{
			fmt.Sprint(s.Seq),
			nz(s.Title, nz(s.VulnClass, "未命名发现")),
			SeverityCN(s.Severity),
			statusCN(s.Status),
		})
	}
	d.Table([]string{"步骤", "漏洞", "严重度", "状态"}, rows)

	outgoing := map[int64][]db.FindingChainEdge{}
	seqByFinding := map[int64]int{}
	for _, s := range c.Steps {
		seqByFinding[s.FindingID] = s.Seq
	}
	for _, e := range c.Edges {
		outgoing[e.Src] = append(outgoing[e.Src], e)
	}

	for _, s := range c.Steps {
		title := nz(s.Title, nz(s.VulnClass, "未命名发现"))
		head := fmt.Sprintf("4.%d [%s] %s%s", s.Seq, SeverityCN(s.Severity), title, chainPhaseCN(s.Seq, len(c.Steps)))
		if hex := SeverityHex(s.Severity); hex != "" {
			d.headingColored(head, 3, hex)
		} else {
			d.H3(head)
		}
		if len(s.AssetIDs) > 0 {
			d.P("影响资产：" + assetNames(s.AssetIDs, names))
		}
		if s.IntentSummary != "" {
			d.P("推进方式：" + chainTruncateText(s.IntentSummary, 300))
		}
		if len(s.Proofs) > 0 {
			d.H4("证明点")
			for _, p := range s.Proofs {
				d.Bullet(p.Label)
			}
		}
		if edges := outgoing[s.FindingID]; len(edges) > 0 {
			parts := make([]string, 0, len(edges))
			for _, e := range edges {
				if seq, ok := seqByFinding[e.Dst]; ok {
					parts = append(parts, fmt.Sprintf("→ 步骤 %d（%s）", seq, chainRelCN(e.Rel)))
				}
			}
			if len(parts) > 0 {
				d.P("后续推进：" + strings.Join(parts, "；"))
			}
		}
		d.P("")
	}

	if pngData := attackChainPNG(c, names); len(pngData) > 0 {
		d.Image(pngData, chartCaption("攻击路径"))
	}
}

// writeAttackChainMarkdown 渲染 Markdown 版「四、攻击路径」章（与 Word 同源）。
func writeAttackChainMarkdown(b *strings.Builder, c *db.AttackChain, names map[int64]string) {
	b.WriteString("## 四、攻击路径\n\n")
	if c == nil || len(c.Steps) == 0 {
		b.WriteString("_本次任务未形成可识别的攻击链：发现尚未绑定产出意图，或链路不足两步。漏洞明细仍完整呈现。_\n\n")
		return
	}
	fmt.Fprintf(b, "本章按探索图血缘还原从初始访问到最终影响的推进路径，共 %d 步、%d 条推进关系。每一步都附可核验的证明点。\n\n",
		len(c.Steps), len(c.Edges))

	b.WriteString("| 步骤 | 漏洞 | 严重度 | 状态 |\n|---|---|---|---|\n")
	for _, s := range c.Steps {
		fmt.Fprintf(b, "| %d | %s | %s | %s |\n", s.Seq, mdCell(nz(s.Title, nz(s.VulnClass, "未命名发现"))),
			SeverityCN(s.Severity), statusCN(s.Status))
	}
	b.WriteString("\n")

	outgoing := map[int64][]db.FindingChainEdge{}
	seqByFinding := map[int64]int{}
	for _, s := range c.Steps {
		seqByFinding[s.FindingID] = s.Seq
	}
	for _, e := range c.Edges {
		outgoing[e.Src] = append(outgoing[e.Src], e)
	}
	for _, s := range c.Steps {
		fmt.Fprintf(b, "### 4.%d [%s] %s%s\n\n", s.Seq, SeverityCN(s.Severity),
			nz(s.Title, nz(s.VulnClass, "未命名发现")), chainPhaseCN(s.Seq, len(c.Steps)))
		if len(s.AssetIDs) > 0 {
			fmt.Fprintf(b, "- **影响资产**：%s\n", assetNames(s.AssetIDs, names))
		}
		if s.IntentSummary != "" {
			fmt.Fprintf(b, "- **推进方式**：%s\n", chainTruncateText(s.IntentSummary, 300))
		}
		for _, p := range s.Proofs {
			fmt.Fprintf(b, "- **证明点**：%s\n", p.Label)
		}
		if edges := outgoing[s.FindingID]; len(edges) > 0 {
			parts := make([]string, 0, len(edges))
			for _, e := range edges {
				if seq, ok := seqByFinding[e.Dst]; ok {
					parts = append(parts, fmt.Sprintf("→ 步骤 %d（%s）", seq, chainRelCN(e.Rel)))
				}
			}
			if len(parts) > 0 {
				fmt.Fprintf(b, "- **后续推进**：%s\n", strings.Join(parts, "；"))
			}
		}
		b.WriteString("\n")
	}
}

func chainTruncateText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// ── PNG 路径图 ────────────────────────────────────────────────────────────

const (
	chainBoxW = 280
	chainBoxH = 108
	chainGapX = 54
	chainGapY = 62
	chainPad  = 36
	chainCols = 3
)

var chainSevColors = map[string][2]color.RGBA{
	"critical": {{0xfe, 0xe2, 0xe2, 0xff}, {0xdc, 0x26, 0x26, 0xff}},
	"high":     {{0xff, 0xed, 0xd5, 0xff}, {0xea, 0x58, 0x0c, 0xff}},
	"medium":   {{0xfe, 0xf9, 0xc3, 0xff}, {0xca, 0x8a, 0x04, 0xff}},
	"low":      {{0xdc, 0xfc, 0xe7, 0xff}, {0x16, 0xa3, 0x4a, 0xff}},
}

// attackChainPNG 把链路画成左到右、按行折返的路径图。字体不可用时返回 nil，
// 调用方静默跳图（章节文字仍在）。
func attackChainPNG(c *db.AttackChain, names map[int64]string) []byte {
	if c == nil || len(c.Steps) == 0 {
		return nil
	}
	fnt := chartCJKFont()
	if fnt == nil {
		return nil
	}
	face := truetype.NewFace(fnt, &truetype.Options{Size: 14, DPI: 72})
	defer face.Close()
	small := truetype.NewFace(fnt, &truetype.Options{Size: 12, DPI: 72})
	defer small.Close()
	titleFace := truetype.NewFace(fnt, &truetype.Options{Size: 20, DPI: 72})
	defer titleFace.Close()

	rows := (len(c.Steps) + chainCols - 1) / chainCols
	w := chainPad*2 + chainCols*chainBoxW + (chainCols-1)*chainGapX
	h := chainPad*2 + 34 + rows*chainBoxH + (rows-1)*chainGapY
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0xff, 0xff, 0xff, 0xff}}, image.Point{}, draw.Src)

	black := image.NewUniform(color.RGBA{0x1f, 0x29, 0x37, 0xff})
	gray := image.NewUniform(color.RGBA{0x4b, 0x55, 0x63, 0xff})
	line := image.NewUniform(color.RGBA{0x94, 0xa3, 0xb8, 0xff})
	drawer := &font.Drawer{Dst: img, Src: black, Face: titleFace}
	drawer.Dot = fixed.P(chainPad, chainPad+18)
	drawer.DrawString("攻击路径")

	boxPos := make([]image.Point, len(c.Steps))
	for i, s := range c.Steps {
		row, col := i/chainCols, i%chainCols
		x := chainPad + col*(chainBoxW+chainGapX)
		y := chainPad + 34 + row*(chainBoxH+chainGapY)
		boxPos[i] = image.Pt(x, y)
		pair, ok := chainSevColors[strings.ToLower(s.Severity)]
		soft, border := pair[0], pair[1]
		if !ok {
			soft = color.RGBA{0xf3, 0xf4, 0xf6, 0xff}
			border = color.RGBA{0x6b, 0x72, 0x80, 0xff}
		}
		draw.Draw(img, image.Rect(x, y, x+chainBoxW, y+chainBoxH), &image.Uniform{soft}, image.Point{}, draw.Src)
		// 边框 + 左侧严重度色条。
		fillRect(img, image.Rect(x, y, x+chainBoxW, y+2), border)
		fillRect(img, image.Rect(x, y+chainBoxH-2, x+chainBoxW, y+chainBoxH), border)
		fillRect(img, image.Rect(x, y, x+2, y+chainBoxH), border)
		fillRect(img, image.Rect(x+chainBoxW-2, y, x+chainBoxW, y+chainBoxH), border)
		fillRect(img, image.Rect(x+2, y+2, x+8, y+chainBoxH-2), border)

		label := fmt.Sprintf("%d. [%s] %s", s.Seq, SeverityCN(s.Severity), nz(s.Title, nz(s.VulnClass, "未命名发现")))
		lines := wrapByWidth(face, label, chainBoxW-30)
		if len(lines) > 2 {
			lines = lines[:2]
			lines[1] = chainTruncateText(lines[1], len([]rune(lines[1]))-1) + "…"
		}
		td := &font.Drawer{Dst: img, Src: black, Face: face}
		ty := y + 28
		for _, ln := range lines {
			td.Dot = fixed.P(x+16, ty)
			td.DrawString(ln)
			ty += 20
		}
		sd := &font.Drawer{Dst: img, Src: gray, Face: small}
		meta := fmt.Sprintf("%s · 证明点 %d", statusCN(s.Status), len(s.Proofs))
		if len(s.AssetIDs) > 0 {
			meta = "资产：" + assetNames(s.AssetIDs, names)
		}
		sd.Dot = fixed.P(x+16, y+chainBoxH-14)
		sd.DrawString(truncateWidth(small, meta, chainBoxW-30))
	}

	// 连线：按步骤顺序连接盒子（同行右→右，跨行折返）。
	for i := 0; i+1 < len(c.Steps); i++ {
		from, to := boxPos[i], boxPos[i+1]
		if from.Y == to.Y {
			drawArrow(img, from.X+chainBoxW, from.Y+chainBoxH/2, to.X, to.Y+chainBoxH/2, line)
		} else {
			// 当前行最后一个 → 下一行第一个：先下折再横移。
			drawArrow(img, from.X+chainBoxW/2, from.Y+chainBoxH, to.X+chainBoxW/2, to.Y, line)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

func drawArrow(img *image.RGBA, x1, y1, x2, y2 int, src *image.Uniform) {
	dx, dy := x2-x1, y2-y1
	steps := dx
	if steps < 0 {
		steps = -steps
	}
	if steps < dy {
		steps = dy
	}
	if steps < 0 {
		steps = -steps
	}
	if steps == 0 {
		return
	}
	for i := 0; i <= steps; i++ {
		x := x1 + dx*i/steps
		y := y1 + dy*i/steps
		img.Set(x, y, src.At(0, 0).(color.RGBA))
		img.Set(x+1, y, src.At(0, 0).(color.RGBA))
	}
	// 箭头（水平/垂直两种方向各画一个三角）。
	const a = 6
	if dx != 0 {
		sign := 1
		if dx < 0 {
			sign = -1
		}
		for i := 0; i < a; i++ {
			img.Set(x2-sign*i, y2-i, src.At(0, 0).(color.RGBA))
			img.Set(x2-sign*i, y2+i, src.At(0, 0).(color.RGBA))
		}
	} else {
		sign := 1
		if dy < 0 {
			sign = -1
		}
		for i := 0; i < a; i++ {
			img.Set(x2-i, y2-sign*i, src.At(0, 0).(color.RGBA))
			img.Set(x2+i, y2-sign*i, src.At(0, 0).(color.RGBA))
		}
	}
}

func wrapByWidth(face font.Face, s string, maxPx int) []string {
	var lines []string
	var cur []rune
	for _, r := range s {
		next := append(append([]rune{}, cur...), r)
		if font.MeasureString(face, string(next)).Round() > maxPx && len(cur) > 0 {
			lines = append(lines, string(cur))
			cur = []rune{r}
			continue
		}
		cur = next
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

func truncateWidth(face font.Face, s string, maxPx int) string {
	if font.MeasureString(face, s).Round() <= maxPx {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && font.MeasureString(face, string(r)).Round() > maxPx {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
