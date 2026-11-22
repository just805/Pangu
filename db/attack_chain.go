package db

// 攻击链（路线图 C1/C2/C3）：把探索图里的 finding 血缘投影成「从初始访问到最终
// 影响」的线性叙事。推导是确定性的、不依赖 LLM：
//
//	上游 finding 节点 --derived_from--> 下游意图   ⇒ derived_from（显式前置）
//	上游 finding 的产出意图是下游意图的血缘祖先   ⇒ enables（凭据/信息推进）
//	同一条意图产出的多个 finding                  ⇒ chained_with（同源并报）
//
// 结果落 finding_chain_edges（source='auto'，可整批重建）；报告章节、任务详情
// 交互图、Word 内嵌图都读同一份数据。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FindingChainEdge 是两个 finding 之间的一条有向推进关系。
type FindingChainEdge struct {
	Src int64  `json:"src"`
	Dst int64  `json:"dst"`
	Rel string `json:"rel"`
}

// AttackChainProof 是链路上某一步的证明点：事实、流量证据或原始证据片段。
type AttackChainProof struct {
	Kind         string `json:"kind"` // fact | traffic | evidence | intent
	Label        string `json:"label"`
	NodeID       int64  `json:"node_id,omitempty"`
	TrafficCount int    `json:"traffic_count,omitempty"`
}

// AttackChainStep 是攻击链上的一步（一条 finding）。
type AttackChainStep struct {
	Seq           int                `json:"seq"`
	FindingID     int64              `json:"finding_id"`
	NodeID        int64              `json:"node_id"`
	Title         string             `json:"title"`
	Severity      string             `json:"severity"`
	VulnClass     string             `json:"vuln_class"`
	Status        string             `json:"status"`
	Summary       string             `json:"summary"`
	Worker        string             `json:"worker"`
	CreatedAt     time.Time          `json:"created_at"`
	AssetIDs      []int64            `json:"asset_ids"`
	IntentID      int64              `json:"intent_id,omitempty"`
	IntentSummary string             `json:"intent_summary,omitempty"`
	Proofs        []AttackChainProof `json:"proofs"`
}

// AttackChain 是任务级的攻击链叙事 + 关系边。
type AttackChain struct {
	Steps []*AttackChainStep `json:"steps"`
	Edges []FindingChainEdge `json:"edges"`
}

type chainNode struct {
	id      int64
	kind    string
	state   string
	payload json.RawMessage
	created time.Time
}

type chainFinding struct {
	id         int64
	nodeID     int64
	title      string
	severity   string
	vulnclass  string
	status     string
	summary    string
	evidence   string
	worker     string
	assetIDs   []int64
	createdAt  time.Time
	producerID int64 // intent that yielded this finding (0 = unknown)
}

type chainQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func chainPayloadField(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func chainTruncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// DeriveFindingChainsTx 重建某探索的自动攻击链边（source='auto'）。幂等：先删后插，
// 手工边（source='manual'）不受影响。写入 finding 与读取报告/图时都会调用。
func DeriveFindingChainsTx(tx *sql.Tx, explorationID int64) error {
	if explorationID <= 0 {
		return nil
	}
	nodes, edges, err := loadChainGraph(tx, explorationID)
	if err != nil {
		return err
	}
	findings, err := loadChainFindings(tx, explorationID)
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		_, err := tx.Exec(`DELETE FROM finding_chain_edges WHERE exploration_id=$1 AND source='auto'`, explorationID)
		return err
	}

	nodeKind := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		nodeKind[n.id] = n.kind
	}
	findingIDByNode := make(map[int64]int64, len(findings))
	byID := make(map[int64]*chainFinding, len(findings))
	for _, f := range findings {
		if f.nodeID > 0 {
			findingIDByNode[f.nodeID] = f.id
		}
		byID[f.id] = f
	}

	parents := make(map[int64][]int64)           // dst → src（血缘上游，排除 covers）
	directFrom := make(map[int64]map[int64]bool) // dst → src（仅 derived_from）
	yieldsOf := make(map[int64][]int64)          // intent → 产出节点
	producer := make(map[int64]int64)            // finding node → 产出意图
	for _, e := range edges {
		switch e.Rel {
		case RelDerivedFrom:
			if directFrom[e.To] == nil {
				directFrom[e.To] = map[int64]bool{}
			}
			directFrom[e.To][e.From] = true
			parents[e.To] = append(parents[e.To], e.From)
		case RelYields:
			yieldsOf[e.From] = append(yieldsOf[e.From], e.To)
			if nodeKind[e.To] == KindFinding {
				producer[e.To] = e.From
			}
			parents[e.To] = append(parents[e.To], e.From)
		case RelSpawns, RelProves:
			parents[e.To] = append(parents[e.To], e.From)
		}
	}
	for nodeID, intentID := range producer {
		if f, ok := byID[findingIDByNode[nodeID]]; ok {
			f.producerID = intentID
		}
	}
	// 意图 → 其产出的 finding（enables 反查用）；同时按意图分组做同源并报。
	siblings := make(map[int64][]int64)
	findingsByIntent := make(map[int64][]int64)
	for _, f := range findings {
		if f.producerID > 0 {
			siblings[f.producerID] = append(siblings[f.producerID], f.id)
			findingsByIntent[f.producerID] = append(findingsByIntent[f.producerID], f.id)
		}
	}

	// ancOf 返回意图的全部血缘祖先（含自身）。图规模小，逐意图 BFS 即可。
	ancCache := make(map[int64]map[int64]bool)
	ancOf := func(intentID int64) map[int64]bool {
		if set, ok := ancCache[intentID]; ok {
			return set
		}
		set := map[int64]bool{intentID: true}
		stack := []int64{intentID}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, p := range parents[cur] {
				if set[p] {
					continue
				}
				set[p] = true
				stack = append(stack, p)
			}
		}
		ancCache[intentID] = set
		return set
	}

	type pairKey struct{ src, dst int64 }
	chosen := make(map[pairKey]string) // 同一对 finding 只保留最强语义
	add := func(src, dst int64, rel string) {
		if src <= 0 || dst <= 0 || src == dst {
			return
		}
		k := pairKey{src, dst}
		if prev, ok := chosen[k]; ok {
			// derived_from > enables > chained_with：新关系更弱或相同则保留旧值。
			if chainRelRank(rel) <= chainRelRank(prev) {
				return
			}
		}
		chosen[k] = rel
	}

	for _, f := range findings {
		if f.nodeID <= 0 || f.producerID <= 0 {
			continue
		}
		anc := ancOf(f.producerID)
		for ancNode := range anc {
			// ① 显式前置：上游 finding 节点本身就是下游意图的 derived_from 父节点。
			if upID, ok := findingIDByNode[ancNode]; ok && upID != f.id {
				rel := "enables"
				if directFrom[f.producerID][ancNode] {
					rel = "derived_from"
				}
				add(upID, f.id, rel)
			}
			// ② 血缘推进：上游意图产出的 finding（finding 节点本身不在祖先集里，
			//    必须从祖先意图的 yields 反查）。
			if nodeKind[ancNode] != KindIntent || ancNode == f.producerID {
				continue
			}
			for _, upID := range findingsByIntent[ancNode] {
				if upID != f.id {
					add(upID, f.id, "enables")
				}
			}
		}
	}

	// 同源并报：同一意图产出的 finding 按 id 顺序串起来（避免 O(n²) 爆炸）。
	for _, ids := range siblings {
		if len(ids) < 2 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for i := 1; i < len(ids); i++ {
			add(ids[i-1], ids[i], "chained_with")
		}
	}

	if _, err := tx.Exec(`DELETE FROM finding_chain_edges WHERE exploration_id=$1 AND source='auto'`, explorationID); err != nil {
		return err
	}
	for k, rel := range chosen {
		if _, err := tx.Exec(`INSERT INTO finding_chain_edges(exploration_id,src_finding_id,dst_finding_id,rel,source)
VALUES($1,$2,$3,$4,'auto') ON CONFLICT DO NOTHING`, explorationID, k.src, k.dst, rel); err != nil {
			return err
		}
	}
	return nil
}

func chainRelRank(rel string) int {
	switch rel {
	case "derived_from":
		return 3
	case "enables":
		return 2
	case "chained_with":
		return 1
	}
	return 0
}

func loadChainGraph(q chainQueryer, explorationID int64) ([]chainNode, []Edge, error) {
	rows, err := q.Query(`SELECT id,kind,state,payload,created_at FROM exploration_nodes WHERE exploration_id=$1 ORDER BY id`, explorationID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var nodes []chainNode
	for rows.Next() {
		var n chainNode
		var raw []byte
		if err := rows.Scan(&n.id, &n.kind, &n.state, &raw, &n.created); err != nil {
			return nil, nil, err
		}
		n.payload = json.RawMessage(raw)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	erows, err := q.Query(`SELECT src_id,rel,dst_id FROM exploration_edges WHERE exploration_id=$1 ORDER BY src_id,dst_id`, explorationID)
	if err != nil {
		return nil, nil, err
	}
	defer erows.Close()
	var edges []Edge
	for erows.Next() {
		var e Edge
		if err := erows.Scan(&e.From, &e.Rel, &e.To); err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	return nodes, edges, erows.Err()
}

func loadChainFindings(q chainQueryer, explorationID int64) ([]*chainFinding, error) {
	rows, err := q.Query(`
SELECT f.id, COALESCE(f.node_id,0), f.name, f.severity, f.vulnclass, f.status,
       f.summary, f.evidence, f.worker, f.asset_ids, f.created_at
FROM findings f
JOIN tasks t ON t.id=f.task_id
WHERE t.exploration_id=$1 AND f.node_id IS NOT NULL
ORDER BY f.created_at, f.id`, explorationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*chainFinding
	for rows.Next() {
		f := &chainFinding{}
		var assetsRaw []byte
		if err := rows.Scan(&f.id, &f.nodeID, &f.title, &f.severity, &f.vulnclass, &f.status,
			&f.summary, &f.evidence, &f.worker, &assetsRaw, &f.createdAt); err != nil {
			return nil, err
		}
		if len(assetsRaw) > 0 {
			_ = json.Unmarshal(assetsRaw, &f.assetIDs)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ensureFindingChains 为历史任务补一次自动边推导：只有当存在 2 条以上 finding 且
// 表里还没有本探索的边时才写，避免每次只读请求都做写事务。
func (s *ExplorationStore) ensureFindingChains(findings int) error {
	if findings < 2 {
		return nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM finding_chain_edges WHERE exploration_id=$1`, s.expID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// 与漏洞写入共用 evidence 事务锁：避免推导的 DELETE+INSERT 与并发上报交错，
	// 也让「报告读到一半链被重建」的窗口消失（推导本身很轻）。
	return s.db.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		return DeriveFindingChainsTx(tx, s.expID)
	})
}

// AttackChain 读取任务攻击链：finding 步骤 + 自动关系边。每一步带证明点
// （产出意图、前置/产出事实、流量证据、证据片段）。没有 finding 时返回空链。
func (s *ExplorationStore) AttackChain() (*AttackChain, error) {
	out := &AttackChain{Steps: []*AttackChainStep{}, Edges: []FindingChainEdge{}}
	findings, err := loadChainFindings(s.db, s.expID)
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		return out, nil
	}
	if err := s.ensureFindingChains(len(findings)); err != nil {
		// 推导失败不拖垮只读视图：继续用已有边渲染。
		_ = err
	}

	nodes, edges, err := loadChainGraph(s.db, s.expID)
	if err != nil {
		return nil, err
	}
	nodeByID := make(map[int64]chainNode, len(nodes))
	for _, n := range nodes {
		nodeByID[n.id] = n
	}
	yieldsOf := make(map[int64][]int64)
	producer := make(map[int64]int64)
	for _, e := range edges {
		if e.Rel == RelYields {
			yieldsOf[e.From] = append(yieldsOf[e.From], e.To)
			if nodeByID[e.To].kind == KindFinding {
				producer[e.To] = e.From
			}
		}
	}

	findingIDByNode := make(map[int64]int64, len(findings))
	for _, f := range findings {
		if f.nodeID > 0 {
			findingIDByNode[f.nodeID] = f.id
		}
		if f.producerID == 0 {
			f.producerID = producer[f.nodeID]
		}
	}

	traffic, err := s.chainTrafficCounts()
	if err != nil {
		traffic = map[int64]int{}
	}
	for _, f := range findings {
		step := &AttackChainStep{
			FindingID: f.id,
			NodeID:    f.nodeID,
			Title:     f.title,
			Severity:  f.severity,
			VulnClass: f.vulnclass,
			Status:    f.status,
			Summary:   f.summary,
			Worker:    f.worker,
			CreatedAt: f.createdAt,
			AssetIDs:  f.assetIDs,
			IntentID:  f.producerID,
			Proofs:    []AttackChainProof{},
		}
		if step.Title == "" {
			step.Title = f.vulnclass
		}
		if step.AssetIDs == nil {
			step.AssetIDs = []int64{}
		}
		if step.IntentID > 0 {
			if n, ok := nodeByID[step.IntentID]; ok {
				step.IntentSummary = chainPayloadField(n.payload, "summary")
				if step.IntentSummary != "" {
					step.Proofs = append(step.Proofs, AttackChainProof{
						Kind: "intent", NodeID: step.IntentID,
						Label: "推进意图：" + chainTruncate(step.IntentSummary, 140),
					})
				}
			}
		}
		// 产出事实（intent --yields--> fact）是这一步最直接的证明点。
		seenFact := map[int64]bool{}
		addFact := func(id int64) {
			if id <= 0 || seenFact[id] {
				return
			}
			seenFact[id] = true
			n := nodeByID[id]
			sum := chainPayloadField(n.payload, "summary")
			label := fmt.Sprintf("事实 #%d", id)
			if sum != "" {
				label += "：" + chainTruncate(sum, 120)
			}
			step.Proofs = append(step.Proofs, AttackChainProof{Kind: "fact", NodeID: id, Label: label})
		}
		for _, id := range yieldsOf[step.IntentID] {
			if nodeByID[id].kind == KindFact {
				addFact(id)
			}
			if len(step.Proofs) >= 5 {
				break
			}
		}
		if n := traffic[f.id]; n > 0 {
			step.Proofs = append(step.Proofs, AttackChainProof{
				Kind: "traffic", TrafficCount: n,
				Label: fmt.Sprintf("已绑定 %d 条流量证据", n),
			})
		}
		if ev := chainTruncate(firstLine(f.evidence), 160); ev != "" {
			step.Proofs = append(step.Proofs, AttackChainProof{Kind: "evidence", Label: "证据：" + ev})
		}
		out.Steps = append(out.Steps, step)
	}

	// 读取已落库的自动/手工边。
	erows, err := s.db.Query(`SELECT src_finding_id,dst_finding_id,rel FROM finding_chain_edges WHERE exploration_id=$1 ORDER BY src_finding_id,dst_finding_id,rel`, s.expID)
	if err != nil {
		return nil, err
	}
	defer erows.Close()
	for erows.Next() {
		var e FindingChainEdge
		if err := erows.Scan(&e.Src, &e.Dst, &e.Rel); err != nil {
			return nil, err
		}
		out.Edges = append(out.Edges, e)
	}
	if err := erows.Err(); err != nil {
		return nil, err
	}

	orderChainSteps(out)
	return out, nil
}

func (s *ExplorationStore) chainTrafficCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`
SELECT b.finding_id, count(*)
FROM finding_traffic_bindings b
JOIN findings f ON f.id=b.finding_id
JOIN tasks t ON t.id=f.task_id
WHERE t.exploration_id=$1
GROUP BY b.finding_id`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// orderChainSteps 按关系边做拓扑排序（Kahn），并列时按发现时间/id 稳定排序；
// 有环（理论上不应出现）时把剩余步骤按时间顺序接在尾部，绝不丢步骤。
func orderChainSteps(c *AttackChain) {
	if c == nil || len(c.Steps) == 0 {
		return
	}
	byID := make(map[int64]*AttackChainStep, len(c.Steps))
	indeg := make(map[int64]int, len(c.Steps))
	adj := make(map[int64][]int64, len(c.Steps))
	for _, s := range c.Steps {
		byID[s.FindingID] = s
	}
	for _, e := range c.Edges {
		if byID[e.Src] == nil || byID[e.Dst] == nil {
			continue
		}
		adj[e.Src] = append(adj[e.Src], e.Dst)
		indeg[e.Dst]++
	}
	less := func(a, b int64) bool {
		x, y := byID[a], byID[b]
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Before(y.CreatedAt)
		}
		return a < b
	}
	var ready []int64
	for _, s := range c.Steps {
		if indeg[s.FindingID] == 0 {
			ready = append(ready, s.FindingID)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
	var ordered []*AttackChainStep
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[id])
		var next []int64
		for _, d := range adj[id] {
			indeg[d]--
			if indeg[d] == 0 {
				next = append(next, d)
			}
		}
		sort.Slice(next, func(i, j int) bool { return less(next[i], next[j]) })
		ready = append(ready, next...)
	}
	if len(ordered) < len(c.Steps) {
		seen := make(map[int64]bool, len(ordered))
		for _, s := range ordered {
			seen[s.FindingID] = true
		}
		var rest []*AttackChainStep
		for _, s := range c.Steps {
			if !seen[s.FindingID] {
				rest = append(rest, s)
			}
		}
		sort.Slice(rest, func(i, j int) bool { return less(rest[i].FindingID, rest[j].FindingID) })
		ordered = append(ordered, rest...)
	}
	for i, s := range ordered {
		s.Seq = i + 1
	}
	c.Steps = ordered
}
