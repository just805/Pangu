package report

// A6 统计图嵌入：严重度/类别分布 PNG 进 Word。图用 go-chart 纯 Go 生成，
// 文字渲染复用内嵌的仿宋_GB2312——Roboto 等默认字体没有 CJK 字形，中文标签
// 会变豆腐块。失败静默降级（返回 nil，调用方跳过图片），绝不拖挂报告导出。

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/golang/freetype/truetype"
	"github.com/just805/pangu/db"
	"github.com/wcharczuk/go-chart/v2"
)

var (
	chartFontOnce sync.Once
	chartFont     *truetype.Font
)

// chartCJKFont 解析内嵌仿宋字体供 go-chart 渲染中文；失败返回 nil（调用方
// 放弃本次图片）。同一文档导出只解析一次。
func chartCJKFont() *truetype.Font {
	chartFontOnce.Do(func() {
		b, err := gongwenFontFS.ReadFile("fonts/fangsong_gb2312.ttf")
		if err != nil {
			return
		}
		chartFont, _ = truetype.Parse(b)
	})
	return chartFont
}

// severityDistChart 严重度分布条形图（PNG；失败返回 nil，调用方跳过图片）。
func severityDistChart(fs []*db.DBFinding) []byte {
	var bars []chart.Value
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := countSev(fs, sev); n > 0 {
			bars = append(bars, chart.Value{Value: float64(n), Label: SeverityCN(sev)})
		}
	}
	if len(bars) == 0 {
		return nil
	}
	out, err := renderBarChart("漏洞严重度分布", bars)
	if err != nil {
		return nil
	}
	return out
}

// classDistChart 漏洞类别分布条形图（按条数降序取前 10；失败返回 nil）。
func classDistChart(fs []*db.DBFinding) []byte {
	type row struct {
		class string
		n     int
	}
	var rows []row
	for _, g := range groupByClass(fs) {
		rows = append(rows, row{g.class, len(g.items)})
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	if len(rows) > 10 {
		rows = rows[:10]
	}
	bars := make([]chart.Value, 0, len(rows))
	for _, r := range rows {
		bars = append(bars, chart.Value{Value: float64(r.n), Label: r.class})
	}
	out, err := renderBarChart("漏洞类别分布（Top 10）", bars)
	if err != nil {
		return nil
	}
	return out
}

// renderBarChart 统一渲染：白底、中文字体、固定尺寸适配 A4 版心。失败返回
// 错误供诊断（调用方静默降级不出图）。
func renderBarChart(title string, bars []chart.Value) ([]byte, error) {
	f := chartCJKFont()
	if f == nil {
		// 字体解析失败：图上中文会变豆腐块，宁可不出图（表格仍在）。
		return nil, fmt.Errorf("chart font unavailable")
	}
	ts := chart.Shown()
	ts.Font = f
	ts.FontSize = 16
	xs := chart.Shown()
	xs.Font = f
	xs.FontSize = 12
	// 全等值（或单条）时默认数据范围为 0 → 报错；显式给 Y 轴范围（0 到 max+1）。
	maxV := 0.0
	for _, v := range bars {
		if v.Value > maxV {
			maxV = v.Value
		}
	}
	ys := chart.Shown()
	ys.Font = f
	ys.FontSize = 11
	// Y 轴整数等距刻度：默认连续轴会给出 14/11/8/6/3/0 这种不整齐的刻度。
	step := math.Max(1, math.Ceil(maxV/4))
	var yticks []chart.Tick
	for v := 0.0; v <= maxV+0.001; v += step {
		yticks = append(yticks, chart.Tick{Value: v, Label: fmt.Sprintf("%.0f", v)})
	}
	// 柱宽原来写死 48、间距 24：只有两根柱时只占左侧 ~150px，右边大片空白，
	// Y 轴刻度被挤到最右侧（用户实测「图表错位」）。改为按柱数铺满版心宽度。
	const (
		chartWidth  = 760
		chartHeight = 300
	)
	usable := chartWidth - 110 // 左右留白 + 右侧 Y 轴刻度区
	spacing := usable / (len(bars) * 3)
	if spacing < 12 {
		spacing = 12
	}
	var buf bytes.Buffer
	bc := chart.BarChart{
		Title:      title,
		TitleStyle: ts,
		Font:       f,
		Width:      chartWidth,
		Height:     chartHeight,
		BarWidth:   spacing * 2,
		BarSpacing: spacing,
		Bars:       bars,
		XAxis:      xs,
		YAxis: chart.YAxis{
			Style: ys,
			Range: &chart.ContinuousRange{Min: 0, Max: maxV + 1},
			Ticks: yticks,
			// 计数轴不要小数点：默认 FloatValueFormatter 会渲染成「2.00」。
			ValueFormatter: chart.ValueFormatter(func(v interface{}) string {
				if fv, ok := v.(float64); ok {
					return fmt.Sprintf("%.0f", fv)
				}
				return fmt.Sprint(v)
			}),
		},
	}
	if err := bc.Render(chart.PNG, &buf); err != nil {
		return nil, err
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("empty chart output")
	}
	return buf.Bytes(), nil
}

// chartCaption 返回题注正文。Image 自身会加「图 N：」序号前缀，这里不能再
// 带「图：」，否则渲染成「图 1：图：漏洞严重度分布」（用户实测）。
func chartCaption(s string) string { return s }
