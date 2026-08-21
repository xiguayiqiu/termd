package termd

// Mermaid 图 → ANSI 字符画渲染。
//
// 定位：终端内把 ```mermaid 代码块渲染成纯文本/ANSI 字符图，不依赖 SVG/PNG
// 或外部 mermaid-cli。支持：
//   - flowchart / graph：TB|TD、LR 方向；节点 A[矩形] A(圆角) A((圆形)) A{菱形}
//     A[[六边形]] A[(圆柱)] A>旗标]；边 --> --- -.- ==> --text--> -->|text| 与链式；
//     %% 注释。
//   - sequenceDiagram：participant / actor / Note / loop / alt / opt / else /
//     -> --> ->> -->> --) --x 等消息线。
//   - 其他图类型优雅降级为原始代码文本（灰底矩形外观不变）。
//
// 输出是带 ANSI 前景色的字符画，宽字符按显示宽度对齐，中文 locale 下列宽稳定。

import (
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
)

// ANSI 配色（256 色）
const (
	mermaidNodeColor = "38;5;117m" // 节点边框/文字：淡蓝
	mermaidAltNode   = "38;5;203m" // 特殊形状节点：珊瑚红
	mermaidEdgeColor = "38;5;79m"  // 连线/箭头：青绿
	mermaidLabelCol  = "38;5;223m" // 边/消息标签：淡黄
	mermaidGroupCol  = "38;5;141m" // 分组标题：淡紫
)

// mermaidCanvas 字符画网格：每格一个 rune + 前景色 SGR。
type mermaidCanvas struct {
	w, h   int
	runes  [][]rune
	fg     [][]string // "38;5;NNNm" 或 ""
	nodeFg string
	edgeFg string
	labFg  string
}

func newMermaidCanvas(h, w int) *mermaidCanvas {
	if h < 1 {
		h = 1
	}
	if w < 1 {
		w = 1
	}
	c := &mermaidCanvas{
		h: h, w: w,
		nodeFg: mermaidNodeColor, edgeFg: mermaidEdgeColor, labFg: mermaidLabelCol,
	}
	c.runes = make([][]rune, h)
	c.fg = make([][]string, h)
	for i := 0; i < h; i++ {
		c.runes[i] = make([]rune, w)
		c.fg[i] = make([]string, w)
		for j := 0; j < w; j++ {
			c.runes[i][j] = ' '
		}
	}
	return c
}

func (c *mermaidCanvas) set(row, col int, r rune, fg string) {
	if row < 0 || row >= c.h || col < 0 || col >= c.w {
		return
	}
	c.runes[row][col] = r
	c.fg[row][col] = fg
}

func (c *mermaidCanvas) setString(row, col int, s string, fg string) {
	for _, r := range s {
		if col >= c.w {
			break
		}
		if r == '\t' {
			col += 4
			continue
		}
		c.set(row, col, r, fg)
		col += runewidth.RuneWidth(r)
	}
}

// hline 画横线（不覆盖已有字符）。
func (c *mermaidCanvas) hline(row, c1, c2 int, ch rune, fg string) {
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	for col := c1; col <= c2; col++ {
		if c.runes[row][col] == ' ' {
			c.runes[row][col] = ch
			c.fg[row][col] = fg
		}
	}
}

// vline 画竖线（不覆盖已有字符）。
func (c *mermaidCanvas) vline(r1, r2, col int, ch rune, fg string) {
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	for r := r1; r <= r2; r++ {
		if c.runes[r][col] == ' ' {
			c.runes[r][col] = ch
			c.fg[r][col] = fg
		}
	}
}

// box 画矩形边框。edges 位：1 顶 2 右 4 底 8 左。w/h 为内部内容宽高。
func (c *mermaidCanvas) box(left, top, w, h int, corners rune, edges int, fg string) {
	right := left + w + 1
	bottom := top + h + 1
	if edges&1 != 0 {
		c.hline(top, left+1, right-1, '-', fg)
		c.set(top, left, corners, fg)
		c.set(top, right, corners, fg)
	}
	if edges&4 != 0 {
		c.hline(bottom, left+1, right-1, '-', fg)
		c.set(bottom, left, corners, fg)
		c.set(bottom, right, corners, fg)
	}
	if edges&8 != 0 {
		c.vline(top+1, bottom-1, left, '|', fg)
	}
	if edges&2 != 0 {
		c.vline(top+1, bottom-1, right, '|', fg)
	}
}

// canvasLines 输出逐行字符串，按格子颜色合并 ANSI 序列。
// 宽字符（中文等）在网格中占 1 格 rune 但终端占 2 列，其后缀空格跳过不输出。
func canvasLines(c *mermaidCanvas) []string {
	out := make([]string, 0, c.h)
	for r := 0; r < c.h; r++ {
		end := c.w
		for end > 0 && c.runes[r][end-1] == ' ' {
			end--
		}
		var b strings.Builder
		curFg := ""
		prevW := 1
		for col := 0; col < end; col++ {
			ch := c.runes[r][col]
			if ch == ' ' && prevW > 1 {
				prevW = 1
				continue // 宽字符第二列占位，不输出空格
			}
			fg := c.fg[r][col]
			if fg != curFg {
				if curFg != "" {
					b.WriteString("\x1b[0m")
				}
				if fg != "" {
					b.WriteString("\x1b[" + fg)
				}
				curFg = fg
			}
			b.WriteRune(ch)
			prevW = runewidth.RuneWidth(ch)
		}
		if curFg != "" {
			b.WriteString("\x1b[0m")
		}
		out = append(out, b.String())
	}
	return out
}

// ============================================================
// 数据结构
// ============================================================

type mermaidNode struct {
	id   string
	text string
	kind rune // '[' 矩形 '(' 圆角 '{' 菱形 'o' 圆形 's' 六边形 'c' 圆柱 'r' 旗标
}

type mermaidEdge struct {
	from, to int
	label    string
	dashed   bool
	thick    bool
	noArrow  bool
	oArrow   bool
	xArrow   bool
}

type mermaidGraph struct {
	dir    string
	nodes  []*mermaidNode
	edges  []*mermaidEdge
	nodeID map[string]int
	groups []*mermaidGroup // subgraph（title + 节点索引集合）
}

type mermaidGroup struct {
	title string
	nodes map[int]bool
}

type seqMsg struct {
	from, to int
	text     string
	solid    bool
	arrow    string
}

type seqMsgRaw struct {
	from, to string
	text     string
	solid    bool
	arrow    string
}

type seqNote struct {
	pos  int // 参与者索引；<0 表示 right of（-pos-1）
	text string
	over bool
}

type seqGroup struct {
	kind      string
	label     string
	start     int // 起始消息索引（该消息前画标签）
	elseAt    int // else 之后第一条消息索引；-1 表示无 else
	elseLabel string
	end       int // 结束消息索引（该消息后画 end）
}

type mermaidSeq struct {
	actorIDs []string // 参与者 ID（消息引用键，对应 actors 的显示名）
	actors   []string // 参与者显示名（`participant X as 名称` 时为 名称）
	messages []*seqMsg
	notes    []*seqNote
	groups   []*seqGroup
	autoNum  bool
}

type mermaidParseResult struct {
	kind  string
	graph *mermaidGraph
	seq   *mermaidSeq
}

// ============================================================
// 解析入口
// ============================================================

func mermaidParse(src string) (*mermaidParseResult, bool) {
	lines := splitClean(src)
	if len(lines) == 0 {
		return nil, false
	}
	first := strings.TrimSpace(lines[0])
	kw := first
	if idx := strings.IndexAny(kw, " \t"); idx >= 0 {
		kw = kw[:idx]
	}
	kw = strings.ToLower(kw)
	res := &mermaidParseResult{}
	switch kw {
	case "graph", "flowchart":
		g := parseFlowchart(lines)
		if g == nil || len(g.nodes) == 0 {
			return nil, false
		}
		res.kind, res.graph = "flowchart", g
		return res, true
	case "sequencediagram", "sequence":
		s := parseSequence(lines)
		if s == nil || len(s.actors) == 0 {
			return nil, false
		}
		res.kind, res.seq = "sequence", s
		return res, true
	}
	return res, false // 其他类型降级
}

func splitClean(src string) []string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ============================================================
// flowchart 解析
// ============================================================

func parseFlowchart(lines []string) *mermaidGraph {
	g := &mermaidGraph{dir: "TB", nodeID: make(map[string]int)}
	first := strings.TrimSpace(lines[0])
	tok := strings.Fields(first)
	if len(tok) < 1 {
		return nil
	}
	if kw := strings.ToLower(tok[0]); kw != "graph" && kw != "flowchart" {
		return nil
	}
	if len(tok) >= 2 {
		switch strings.ToUpper(tok[1]) {
		case "TB", "TD":
			g.dir = "TB"
		case "LR":
			g.dir = "LR"
		case "BT", "RL":
			g.dir = strings.ToUpper(tok[1])
		}
	}
	ensureNode := func(id string) int {
		if i, ok := g.nodeID[id]; ok {
			return i
		}
		g.nodes = append(g.nodes, &mermaidNode{id: id, text: id, kind: '['})
		g.nodeID[id] = len(g.nodes) - 1
		return len(g.nodes) - 1
	}
	// 第一遍：收集节点定义（跳过边行，防止误解析）；同时跟踪 subgraph 分组
	var gstack []*mermaidGroup
	for _, l := range lines[1:] {
		t := normalizeEdgeLine(strings.TrimSpace(l))
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		if strings.HasPrefix(t, "subgraph") {
			grp := &mermaidGroup{title: parseSubgraphTitle(t), nodes: make(map[int]bool)}
			g.groups = append(g.groups, grp)
			gstack = append(gstack, grp)
			continue
		}
		if t == "end" {
			if len(gstack) > 0 {
				gstack = gstack[:len(gstack)-1]
			}
			continue
		}
		if isEdgeLine(t) {
			continue
		}
		if nd := parseNodeDef(t); nd != nil {
			idx, ok := g.nodeID[nd.id]
			if !ok {
				g.nodes = append(g.nodes, nd)
				idx = len(g.nodes) - 1
				g.nodeID[nd.id] = idx
			}
			for _, grp := range gstack {
				grp.nodes[idx] = true
			}
		}
	}
	// 第二遍：解析边（同时按 subgraph 栈把边涉及的节点标记到分组）
	grpIdx := 0
	var gstack2 []*mermaidGroup
	for _, l := range lines[1:] {
		t := normalizeEdgeLine(strings.TrimSpace(l))
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		if strings.HasPrefix(t, "subgraph") {
			if grpIdx < len(g.groups) {
				gstack2 = append(gstack2, g.groups[grpIdx])
				grpIdx++
			}
			continue
		}
		if t == "end" {
			if len(gstack2) > 0 {
				gstack2 = gstack2[:len(gstack2)-1]
			}
			continue
		}
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "classdef") || strings.HasPrefix(low, "class ") ||
			strings.HasPrefix(low, "style ") || strings.HasPrefix(low, "linkstyle") ||
			strings.HasPrefix(low, "click ") || strings.HasPrefix(low, "direction ") {
			continue
		}
		if !isEdgeLine(t) {
			continue
		}
		involved := parseEdgeLine(g, t, ensureNode)
		for _, ni := range involved {
			for _, grp := range gstack2 {
				grp.nodes[ni] = true
			}
		}
	}
	return g
}

// parseSubgraphTitle 提取 subgraph 标题：subgraph id[标题] / subgraph id[标题]。
func parseSubgraphTitle(t string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "subgraph"))
	if i := strings.Index(s, "["); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
		if j := strings.Index(s, "]"); j >= 0 {
			s = s[:j]
		}
		s = strings.Trim(s, "\"'")
	}
	return s
}

// normalizeEdgeLine 把虚线带标签形态 `A -. text .-> B` 规范化为 `A -.->|text| B`。
func normalizeEdgeLine(t string) string {
	if !strings.Contains(t, ".->") {
		return t
	}
	if i := strings.Index(t, "-."); i >= 0 {
		if j := strings.Index(t[i:], ".->"); j >= 0 {
			j += i
			if j <= i+2 {
				return t // 无标签虚线 `A -.-> B`，无需规范化
			}
			label := strings.TrimSpace(t[i+2 : j])
			if label == "" {
				return t
			}
			return t[:i] + "-.->|" + label + "|" + t[j+3:]
		}
	}
	return t
}

func parseNodeDef(s string) *mermaidNode {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	pos := -1
	for i, r := range s {
		if r == '[' || r == '(' || r == '{' || r == '>' {
			pos = i
			break
		}
	}
	if pos <= 0 {
		return nil
	}
	id := strings.Trim(strings.TrimSpace(s[:pos]), "\"'")
	if id == "" {
		return nil
	}
	rest := s[pos:]
	var kind rune
	var text string
	switch rest[0] {
	case '[':
		if strings.HasPrefix(rest, "[[") {
			kind = 's'
			if end := strings.Index(rest, "]]"); end >= 0 {
				text = strings.TrimSpace(rest[2:end])
			}
		} else if end := strings.Index(rest, "]"); end >= 0 {
			kind = '['
			text = strings.TrimSpace(rest[1:end])
		}
	case '(':
		if strings.HasPrefix(rest, "((") {
			kind = 'o'
			if end := strings.Index(rest, "))"); end >= 0 {
				text = strings.TrimSpace(rest[2:end])
			}
		} else if strings.HasPrefix(rest, "([") {
			kind = 'c'
			if end := strings.Index(rest, ")]"); end >= 0 {
				text = strings.TrimSpace(rest[2:end])
			}
		} else if end := strings.Index(rest, ")"); end >= 0 {
			kind = '('
			text = strings.TrimSpace(rest[1:end])
		}
	case '{':
		if end := strings.Index(rest, "}"); end >= 0 {
			kind = '{'
			text = strings.TrimSpace(rest[1:end])
		}
	case '>':
		if end := strings.Index(rest, "]"); end >= 0 {
			kind = 'r'
			text = strings.TrimSpace(rest[1:end])
		}
	}
	if text == "" {
		text = id
	}
	return &mermaidNode{id: id, text: strings.Trim(text, "\"'"), kind: kind}
}

func isEdgeLine(t string) bool {
	return strings.Contains(t, "-->") || strings.Contains(t, "---") ||
		strings.Contains(t, "-.-") || strings.Contains(t, "==>") ||
		strings.Contains(t, "=>") || strings.Contains(t, "-.->") ||
		strings.Contains(t, "--x") || strings.Contains(t, "--o")
}

type edgeToken struct {
	pos, end int
	dashed   bool
	thick    bool
	noArrow  bool
	oArrow   bool
	xArrow   bool
}

// findEdgeToken 查找第一个边标记（长标记优先：-.-> 优先于 -.-）。
func findEdgeToken(s string) *edgeToken {
	best := -1
	var tok *edgeToken
	try := func(i, l int, init func(*edgeToken)) {
		if best < 0 || i < best {
			best = i
			tok = &edgeToken{pos: i, end: i + l}
			init(tok)
		}
	}
	for i := 0; i+3 < len(s); i++ {
		if strings.HasPrefix(s[i:i+4], "-.->") {
			try(i, 4, func(t *edgeToken) { t.dashed = true })
		}
	}
	for i := 0; i+2 < len(s); i++ {
		ch := s[i : i+3]
		switch {
		case strings.HasPrefix(ch, "-->"):
			try(i, 3, func(t *edgeToken) {})
		case strings.HasPrefix(ch, "--o"):
			try(i, 3, func(t *edgeToken) { t.oArrow = true })
		case strings.HasPrefix(ch, "--x"):
			try(i, 3, func(t *edgeToken) { t.xArrow = true })
		case strings.HasPrefix(ch, "-.-"):
			try(i, 3, func(t *edgeToken) { t.dashed = true })
		case strings.HasPrefix(ch, "==>"):
			try(i, 3, func(t *edgeToken) { t.thick = true })
		case strings.HasPrefix(ch, "---"):
			try(i, 3, func(t *edgeToken) { t.noArrow = true })
		}
	}
	return tok
}

// parseEdgeLine 解析边行（支持链式 A --> B --> C、行内节点定义 A[text] --> B{text}、
// 标签 --text--> 与 -->|text|、虚线标签 -. text .->）。
func parseEdgeLine(g *mermaidGraph, line string, ensureNode func(string) int) []int {
	var involved []int
	rest := line
	for len(rest) > 0 {
		e := findEdgeToken(rest)
		if e == nil {
			break
		}
		head := strings.TrimSpace(rest[:e.pos])
		headLabel := ""
		from := resolveNode(g, head, ensureNode)
		if from >= 0 {
			// head 可能带 `-- label`（`B -- yes --> C` 形态，标签在箭头标记之前）
			if id, lbl := splitHeadLabel(head); id != "" && lbl != "" {
				headLabel = lbl
			}
		} else {
			id, lbl := splitHeadLabel(head)
			if id == "" {
				break
			}
			from = resolveNode(g, id, ensureNode)
			if from < 0 {
				break
			}
			headLabel = lbl
		}
		involved = append(involved, from)
		tail := strings.TrimSpace(rest[e.end:])
		to, label, nextRest := resolveTailNode(g, tail, ensureNode)
		if to < 0 {
			break
		}
		if label == "" {
			label = headLabel
		}
		involved = append(involved, to)
		g.edges = append(g.edges, &mermaidEdge{
			from: from, to: to, label: label,
			dashed: e.dashed, thick: e.thick, noArrow: e.noArrow,
			oArrow: e.oArrow, xArrow: e.xArrow,
		})
		rest = nextRest
	}
	return involved
}

// splitHeadLabel 拆 `ID -- label` / `ID -. label` / `ID ==> label` 形态（标签在边标记前）：
// 返回 (id 部分, label 部分)。head 无标记时返回 (head, "")。
func splitHeadLabel(head string) (string, string) {
	best := -1
	for _, sep := range []string{"--", "-.", "==", "-o", "-x"} {
		if i := strings.Index(head, sep); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		return head, ""
	}
	id := strings.TrimSpace(head[:best])
	rest := strings.TrimLeft(head[best:], "-.=")
	rest = strings.Trim(rest, " \t")
	if id == "" {
		return "", ""
	}
	return id, rest
}

// resolveNode 把节点段解析为节点索引：优先识别形状定义（A[text]），否则按 id。
func resolveNode(g *mermaidGraph, s string, ensureNode func(string) int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	if nd := parseNodeDef(s); nd != nil {
		if i, ok := g.nodeID[nd.id]; ok {
			return i
		}
		g.nodes = append(g.nodes, nd)
		g.nodeID[nd.id] = len(g.nodes) - 1
		return len(g.nodes) - 1
	}
	id, _ := takeID(s)
	if id == "" {
		return -1
	}
	return ensureNode(id)
}

// resolveTailNode 解析箭头后的一段（to 节点 + 可能的标签）。
// 返回 (to 索引, 标签, 剩余串)。标签来源：|label| 或 -- label --> 形态。
func resolveTailNode(g *mermaidGraph, tail string, ensureNode func(string) int) (int, string, string) {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return -1, "", ""
	}
	label := ""
	// -->|label| B 形态
	if strings.HasPrefix(tail, "|") {
		if close := strings.Index(tail[1:], "|"); close >= 0 {
			label = strings.TrimSpace(tail[1 : 1+close])
			tail = strings.TrimSpace(tail[2+close:])
			if tail == "" {
				return -1, "", ""
			}
		}
	}
	// 找本段结束：下一个边标记（链式）或字符串尾
	segEnd := len(tail)
	if e2 := findEdgeToken(tail); e2 != nil {
		segEnd = e2.pos
	}
	seg := strings.TrimSpace(tail[:segEnd])
	nextRest := strings.TrimSpace(tail[segEnd:])
	if seg == "" {
		return -1, "", ""
	}
	// seg 可能是 "B"、"B{text}"、"label B"（-- label --> 形态，label 在前）
	toID := seg
	if parseNodeDef(seg) == nil && isPlainID(seg) {
		toID = seg
	} else if parseNodeDef(seg) == nil && strings.ContainsAny(seg, " \t") {
		// 拆成 label + 节点（取最后一个词为节点）
		fields := strings.Fields(seg)
		if len(fields) >= 2 {
			if label == "" {
				label = strings.Join(fields[:len(fields)-1], " ")
			}
			toID = fields[len(fields)-1]
		}
	}
	to := resolveNode(g, toID, ensureNode)
	return to, label, nextRest
}

// isPlainID 判断是否纯标识符（不含空格/形状符）。
func isPlainID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '[' || r == '(' || r == '{' || r == '>' {
			return false
		}
	}
	return true
}

// takeID 提取行首节点 id（引号包裹或到分隔符为止）。
func takeID(s string) (id, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	i := 0
	if s[0] == '"' || s[0] == '\'' {
		q := s[0]
		i = 1
		for i < len(s) && s[i] != q {
			i++
		}
		i++
	} else {
		for i < len(s) {
			ch := s[i]
			if ch == ' ' || ch == '\t' || ch == '-' || ch == '=' || ch == '>' ||
				ch == '<' || ch == '|' || ch == '{' || ch == '}' || ch == '[' ||
				ch == ']' || ch == '(' || ch == ')' || ch == '&' || ch == '.' {
				break
			}
			i++
		}
	}
	id = strings.Trim(strings.TrimSpace(s[:i]), "\"'")
	rest = strings.TrimSpace(s[i:])
	return id, rest
}

// ============================================================
// 对外入口
// ============================================================

// RenderMermaid 渲染 mermaid 源文本为 ANSI 字符画。
// ok=false 表示类型不支持或解析失败，调用方应降级为原始代码块显示。
func RenderMermaid(src string, availWidth int) ([]string, bool) {
	if availWidth < 12 {
		availWidth = 12
	}
	res, ok := mermaidParse(src)
	if !ok || res == nil {
		return nil, false
	}
	var lines []string
	switch res.kind {
	case "flowchart":
		lines = renderFlowchart(res.graph, availWidth)
	case "sequence":
		lines = renderSequence(res.seq, availWidth)
	default:
		return nil, false
	}
	if len(lines) == 0 {
		return nil, false
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, true
}

// IsMermaidFence 判断代码块语言标注是否为 mermaid。
func IsMermaidFence(lang string) bool {
	t := strings.TrimSpace(lang)
	if t == "" {
		return false
	}
	first := strings.Fields(t)[0]
	first = strings.Trim(first, "\"'")
	return strings.EqualFold(first, "mermaid")
}

// ============================================================
// flowchart 渲染
// ============================================================

func renderFlowchart(g *mermaidGraph, availWidth int) []string {
	if g.dir == "LR" {
		return layoutHorizontal(g, availWidth)
	}
	return layoutVertical(g, availWidth)
}

// nodeBox 计算节点显示尺寸：宽（含左右边框）、高（含上下边框）。
func nodeBox(nd *mermaidNode, vertical bool) (int, int) {
	w := runewidth.StringWidth(nd.text)
	if w < 4 {
		w = 4
	}
	if w > 30 {
		w = 30
	}
	lines := 1
	if vertical && w > 24 {
		lines = 2
		if w > 48 {
			w = 48
		}
	}
	h := lines + 2 // 上下边框
	if nd.kind == '{' {
		h = lines + 4 // 菱形上下留尖
	}
	return w + 2, h
}

// buildChains 把节点拆成链（入度为 0 的节点出发，沿边走到头）。
func buildChains(g *mermaidGraph) [][]int {
	n := len(g.nodes)
	inDeg := make([]int, n)
	for _, e := range g.edges {
		if e.from >= 0 && e.from < n && e.to >= 0 && e.to < n {
			inDeg[e.to]++
		}
	}
	used := make([]bool, n)
	var chains [][]int
	for i := 0; i < n; i++ {
		if used[i] || inDeg[i] > 0 {
			continue
		}
		var ch []int
		cur := i
		for cur >= 0 && !used[cur] {
			used[cur] = true
			ch = append(ch, cur)
			next := -1
			for _, e := range g.edges {
				if e.from == cur && e.to >= 0 && !used[e.to] {
					next = e.to
					break
				}
			}
			cur = next
		}
		if len(ch) > 0 {
			chains = append(chains, ch)
		}
	}
	for i := 0; i < n; i++ {
		if !used[i] {
			chains = append(chains, []int{i})
		}
	}
	// 长链优先
	for i := 1; i < len(chains); i++ {
		for j := i; j > 0 && len(chains[j]) > len(chains[j-1]); j-- {
			chains[j], chains[j-1] = chains[j-1], chains[j]
		}
	}
	return chains
}

func findEdgeBetween(g *mermaidGraph, from, to int) *mermaidEdge {
	for _, e := range g.edges {
		if e.from == from && e.to == to {
			return e
		}
	}
	return nil
}

// drawNode 绘制节点（居中文本 + 形状 + 边框色）。
func drawNode(c *mermaidCanvas, nd *mermaidNode, w, h, x, y int) {
	fg := c.nodeFg
	switch nd.kind {
	case '{', 'o', 's', 'c', 'r':
		fg = mermaidAltNode
	}
	text := nd.text
	tw := runewidth.StringWidth(text)
	if tw > w-2 {
		text = truncateRunes(text, w-2)
		tw = runewidth.StringWidth(text)
	}
	tx := x + (w-tw)/2
	ty := y + h/2
	if h >= 3 {
		ty = y + h/2 // 内容居中偏下
	}
	c.setString(ty, tx, text, fg)
	last := y + h - 1
	switch nd.kind {
	case '[': // 矩形
		c.set(y, x, '+', fg)
		c.hline(y, x+1, x+w-2, '-', fg)
		c.set(y, x+w-1, '+', fg)
		c.vline(y+1, last-1, x, '|', fg)
		c.vline(y+1, last-1, x+w-1, '|', fg)
		c.set(last, x, '+', fg)
		c.hline(last, x+1, x+w-2, '-', fg)
		c.set(last, x+w-1, '+', fg)
	case '(': // 圆角
		c.set(y, x, '.', fg)
		c.hline(y, x+1, x+w-2, '-', fg)
		c.set(y, x+w-1, '.', fg)
		c.vline(y+1, last-1, x, '|', fg)
		c.vline(y+1, last-1, x+w-1, '|', fg)
		c.set(last, x, '\'', fg)
		c.hline(last, x+1, x+w-2, '-', fg)
		c.set(last, x+w-1, '\'', fg)
	case 'o': // 圆形
		c.set(y, x, '.', fg)
		c.hline(y, x+1, x+w-2, '-', fg)
		c.set(y, x+w-1, '.', fg)
		c.vline(y+1, last-1, x, '(', fg)
		c.vline(y+1, last-1, x+w-1, ')', fg)
		c.set(last, x, '`', fg)
		c.hline(last, x+1, x+w-2, '-', fg)
		c.set(last, x+w-1, '\'', fg)
	case '{': // 菱形
		mid := x + w/2
		c.set(y, mid, '^', fg)
		for i := 1; i < h/2; i++ {
			l := mid - i
			rr := mid + i
			if l >= x {
				c.set(y+i, l, '/', fg)
				c.set(last-i, l, '\\', fg)
			}
			if rr <= x+w-1 {
				c.set(y+i, rr, '\\', fg)
				c.set(last-i, rr, '/', fg)
			}
		}
		c.set(last, mid, 'v', fg)
		ty = y + h/2
		c.setString(ty, tx, text, fg)
	case 's': // 六边形
		c.set(y, x+1, '/', fg)
		c.hline(y, x+2, x+w-3, '-', fg)
		c.set(y, x+w-2, '\\', fg)
		c.vline(y+1, last-1, x, '|', fg)
		c.vline(y+1, last-1, x+w-1, '|', fg)
		c.set(last, x+1, '\\', fg)
		c.hline(last, x+2, x+w-3, '-', fg)
		c.set(last, x+w-2, '/', fg)
	case 'c': // 圆柱
		c.hline(y, x, x+w-1, '_', fg)
		c.vline(y+1, last-1, x, '|', fg)
		c.vline(y+1, last-1, x+w-1, '|', fg)
		c.hline(last, x, x+w-1, '_', fg)
	case 'r': // 旗标
		c.set(y, x, '>', fg)
		c.hline(y, x+1, x+w-2, '-', fg)
		c.set(y, x+w-1, '|', fg)
		c.vline(y+1, last-1, x, '>', fg)
		c.vline(y+1, last-1, x+w-1, '|', fg)
		c.set(last, x, '>', fg)
		c.hline(last, x+1, x+w-2, '-', fg)
		c.set(last, x+w-1, '|', fg)
	}
}

// layoutVertical：TD/TB 分层布局。
// 1) 拓扑分层：入度 0 的节点在第 0 层，边 from->to 保证 to 层号严格更大。
// 2) 同一层节点水平排列（barycenter 启发式减少边交叉）。
// 3) 层间画边：竖线+水平段+箭头，from 底部中心 → to 顶部中心；
//    分支边（fan-out）出口错开，所有边由 mermaidEdge.from/to 驱动。
func layoutVertical(g *mermaidGraph, availWidth int) []string {
	n := len(g.nodes)
	if n == 0 {
		return nil
	}
	level := computeLevels(g, n)
	maxL := 0
	for _, lv := range level {
		if lv > maxL {
			maxL = lv
		}
	}
	layerNodes := make([][]int, maxL+1)
	for i, lv := range level {
		layerNodes[lv] = append(layerNodes[lv], i)
	}
	origW := make([]int, n)
	origH := make([]int, n)
	for i, nd := range g.nodes {
		origW[i], origH[i] = nodeBox(nd, true)
	}
	// subgraph 标题：每层任一节点属于某分组即在该层上方显示标题（宽松判断）；
	// 相邻层相同标题去重（subgraph 跨多层时只显示一次）。
	layerTitle := make([]string, maxL+1)
	for l := 0; l <= maxL; l++ {
		layerTitle[l] = layerGroupTitle(g, layerNodes[l])
		if l > 0 && layerTitle[l] == layerTitle[l-1] {
			layerTitle[l] = ""
		}
	}
	hasTitle := make([]bool, maxL+1)
	for l := 0; l <= maxL; l++ {
		hasTitle[l] = layerTitle[l] != ""
	}

	// 每层最大节点高：跨层边绕行需要据此判断盒子行区间
	layerH := make([]int, maxL+1)
	for l := 0; l <= maxL; l++ {
		for _, ni := range layerNodes[l] {
			if origH[ni] > layerH[l] {
				layerH[l] = origH[ni]
			}
		}
	}

	// 每节点出边数（fan-out 时每条出边占一行标签/水平段，动态增加层间间隙）
	outCnt := make([]int, n)
	maxOut := 0
	for _, e := range g.edges {
		if e.from >= 0 && e.from < n && e.to >= 0 && e.to < n {
			outCnt[e.from]++
			if outCnt[e.from] > maxOut {
				maxOut = outCnt[e.from]
			}
		}
	}
	gapRows := 3 // 基础间隙行数：竖线/水平段/箭头
	if maxOut > 2 {
		gapRows += maxOut - 2 // fan-out 每条出边多占一行
	}
	var layerY []int // 每层顶部行
	var nodeX []int  // 每节点左列
	place := func(scale float64) (maxX, totalH int) {
		scaledW := make([]int, n)
		scaledH := make([]int, n)
		for i := 0; i < n; i++ {
			sw := int(float64(origW[i]) * scale)
			if sw < 4 {
				sw = 4
			}
			scaledW[i] = sw
			scaledH[i] = origH[i]
		}
		layerY = make([]int, maxL+1)
		y := 0
		for l := 0; l <= maxL; l++ {
			if layerTitle[l] != "" {
				y++ // 标题行
			}
			layerY[l] = y
			mh := 0
			for _, ni := range layerNodes[l] {
				if scaledH[ni] > mh {
					mh = scaledH[ni]
				}
			}
			y += mh + gapRows
		}
		totalH = y - gapRows + 2
		nodeX = make([]int, n)
		for l := 0; l <= maxL; l++ {
			nodes := layerNodes[l]
			// barycenter 排序：按上一层邻居的平均 x 升序，减少边交叉
			sort.SliceStable(nodes, func(i, j int) bool {
				ax, okA := barycenter(g, nodes[i], level, nodeX)
				bx, okB := barycenter(g, nodes[j], level, nodeX)
				if okA != okB {
					return okA
				}
				if okA && ax != bx {
					return ax < bx
				}
				return nodes[i] < nodes[j]
			})
			cx := 1
			for _, ni := range nodes {
				nodeX[ni] = cx
				cx += scaledW[ni] + 2
			}
			if cx > maxX {
				maxX = cx
			}
		}
		return maxX, totalH
	}
	// 超宽时逐步缩小节点，直到放下（最小 0.4 倍）
	scale := 1.0
	for {
		maxX, _ := place(scale)
		if maxX <= availWidth || scale <= 0.4 {
			break
		}
		scale -= 0.1
	}
	_, totalH := place(scale)

	canvas := newMermaidCanvas(totalH, availWidth)
	scaledW := make([]int, n)
	for i := 0; i < n; i++ {
		sw := int(float64(origW[i]) * scale)
		if sw < 4 {
			sw = 4
		}
		scaledW[i] = sw
	}
	// 先画节点与分组标题
	for l := 0; l <= maxL; l++ {
		if t := layerTitle[l]; t != "" && layerY[l] > 0 {
			canvas.setString(layerY[l]-1, 1, t, mermaidGroupCol)
		}
		for _, ni := range layerNodes[l] {
			drawNode(canvas, g.nodes[ni], scaledW[ni], origH[ni], nodeX[ni], layerY[l])
		}
	}
	// 再画边（后画，跨层边可覆盖中间层空白；fan-out 出口错开）
	outIdx := make([]int, n)
	for _, e := range g.edges {
		if e.from < 0 || e.from >= n || e.to < 0 || e.to >= n {
			continue
		}
		if level[e.to] <= level[e.from] {
			continue // 同层/反向边不画（环等异常图）
		}
		drawLayeredEdge(canvas, level, nodeX, scaledW, origH, layerY, layerH, layerNodes, hasTitle,
			e, outCnt[e.from], outIdx[e.from])
		outIdx[e.from]++
	}
	return canvasLines(canvas)
}

// computeLevels 计算拓扑层号：入度 0 的节点为第 0 层，
// 边 from->to 使 level[to] >= level[from]+1；环内剩余节点统一放到最后一层。
func computeLevels(g *mermaidGraph, n int) []int {
	level := make([]int, n)
	deg := make([]int, n)
	for _, e := range g.edges {
		if e.from >= 0 && e.from < n && e.to >= 0 && e.to < n {
			deg[e.to]++
		}
	}
	var q []int
	for i := 0; i < n; i++ {
		if deg[i] == 0 {
			q = append(q, i)
		}
	}
	for len(q) > 0 {
		u := q[0]
		q = q[1:]
		for _, e := range g.edges {
			if e.from != u || e.to < 0 || e.to >= n {
				continue
			}
			if level[e.to] < level[u]+1 {
				level[e.to] = level[u] + 1
			}
			deg[e.to]--
			if deg[e.to] == 0 {
				q = append(q, e.to)
			}
		}
	}
	maxL := 0
	for i := 0; i < n; i++ {
		if deg[i] == 0 && level[i] > maxL {
			maxL = level[i]
		}
	}
	for i := 0; i < n; i++ {
		if deg[i] > 0 {
			level[i] = maxL + 1
		}
	}
	return level
}

// barycenter 返回节点在上一层邻居的平均 x 列；无上一层邻居返回 false。
func barycenter(g *mermaidGraph, ni int, level, nodeX []int) (int, bool) {
	sum, cnt := 0, 0
	for _, e := range g.edges {
		if e.from == ni && e.to >= 0 && e.to < len(level) && level[e.to] == level[ni]-1 {
			sum += nodeX[e.to]
			cnt++
		}
		if e.to == ni && e.from >= 0 && e.from < len(level) && level[e.from] == level[ni]-1 {
			sum += nodeX[e.from]
			cnt++
		}
	}
	if cnt == 0 {
		return 0, false
	}
	return sum / cnt, true
}

// drawLayeredEdge 绘制分层布局中的一条边：from 底部中心 → to 顶部中心。
// outIdx 用于 fan-out 出口错开，避免多条出边完全重叠。
// hasTitle 标记层顶是否有 subgraph 标题行（有则箭头行让位，画在标题行之上）。
// 跨层边（中间隔着其他层）的竖线段若穿过中间层节点盒子，会在盒子顶部上方的
// 间隙行水平绕行到空白列，避免垂直竖线破坏节点显示。
func drawLayeredEdge(c *mermaidCanvas, level, nodeX, nodeW, nodeH, layerY, layerH []int,
	layerNodes [][]int, hasTitle []bool, e *mermaidEdge, outCnt, outIdx int) {
	from, to := e.from, e.to
	n := len(nodeX)
	if from < 0 || from >= n || to < 0 || to >= n {
		return
	}
	fx := nodeX[from] + nodeW[from]/2
	fy := layerY[level[from]] + nodeH[from] - 1
	tx := nodeX[to] + nodeW[to]/2
	ty := layerY[level[to]]
	if ty <= fy {
		return
	}
	// fan-out 出口偏移：以 from 底部中心为对称轴均匀分布（间距 4），
	// 避免兄弟边竖线盖住标签，也避免窄节点时出口越界
	exit := fx
	if outCnt > 1 {
		exit = fx + (outIdx*2-outCnt+1)*2
	}
	if exit < 1 {
		exit = 1
	}
	if exit >= c.w-1 {
		exit = c.w - 2
	}
	arrowRow := ty - 1
	if hasTitle[level[to]] {
		arrowRow = ty - 2 // 层顶是标题行，箭头让位到标题之上
	}
	horizRow := arrowRow - 1
	if horizRow <= fy {
		horizRow = fy + 1
	}
	// fan-out 多条出边的水平段分行（band 逐行下移），避免线段与标签互相覆盖
	band := horizRow
	if outCnt > 1 {
		band = fy + 1 + outIdx
		if band > horizRow {
			band = horizRow
		}
	}

	drawEV := func(r, col int) {
		ch := '|'
		if e.thick {
			ch = '='
		} else if e.dashed && r%2 == 0 {
			ch = '.'
		}
		c.set(r, col, ch, c.edgeFg)
	}
	drawEH := func(row, c1, c2 int, keepEnds bool) {
		lo, hi := c1, c2
		if lo > hi {
			lo, hi = hi, lo
		}
		for col := lo; col <= hi; col++ {
			if keepEnds && (col == c1 || col == c2) {
				continue
			}
			ch := '-'
			if e.thick {
				ch = '='
			}
			if e.dashed && (col-lo)%3 == 0 {
				ch = '.'
			}
			c.set(row, col, ch, c.edgeFg)
		}
	}

	// 中间层节点盒子区间：跨层边的竖线可能穿过它们
	type mbox struct {
		l                      int
		top, bottom, left, right int
	}
	var boxes []mbox
	for l := level[from] + 1; l < level[to]; l++ {
		top := layerY[l]
		bottom := layerY[l] + layerH[l] - 1
		if bottom < fy {
			continue
		}
		for _, ni := range layerNodes[l] {
			boxes = append(boxes, mbox{l, top, bottom, nodeX[ni], nodeX[ni] + nodeW[ni] - 1})
		}
	}
	firstHit := func(col, r1, r2 int) (mbox, bool) {
		var best mbox
		ok := false
		for _, b := range boxes {
			if col >= b.left && col <= b.right && r1 <= b.bottom && r2 >= b.top {
				if !ok || b.top < best.top {
					best = b
					ok = true
				}
			}
		}
		return best, ok
	}
	nearestFree := func(col int) int {
		w := c.w
		for d := 1; d < w; d++ {
			for _, s := range []int{-1, 1} {
				cc := col + s*d
				if cc < 1 || cc >= w-1 {
					continue
				}
				free := true
				for _, b := range boxes {
					if cc >= b.left && cc <= b.right {
						free = false
						break
					}
				}
				if free {
					return cc
				}
			}
		}
		return col
	}

	// 正交布线：垂直段遇中间盒子绕行（在盒子顶部上方间隙行水平移动）
	type vseg struct{ col, r1, r2 int }
	type hseg struct{ row, c1, c2 int }
	detour := func(s vseg) ([]vseg, []hseg) {
		var vs []vseg
		var hs []hseg
		pend := s
		for {
			b, hit := firstHit(pend.col, pend.r1, pend.r2)
			if !hit {
				if pend.r1 <= pend.r2 {
					vs = append(vs, pend)
				}
				return vs, hs
			}
			cut := b.top - 1
			if hasTitle[b.l] {
				cut = b.top - 2 // 层顶是标题行，绕行段让位到标题之上
			}
			if cut < pend.r1 {
				cut = pend.r1
			}
			if cut > pend.r2 {
				cut = pend.r2
			}
			if pend.r1 <= cut {
				vs = append(vs, vseg{pend.col, pend.r1, cut})
			}
			newx := nearestFree(pend.col)
			if newx == pend.col || cut+1 > pend.r2 {
				// 无空白列或已到段尾：剩余部分原列画完（穿透，极端情况）
				if cut+1 <= pend.r2 {
					vs = append(vs, vseg{pend.col, cut + 1, pend.r2})
				}
				return vs, hs
			}
			hs = append(hs, hseg{cut, pend.col, newx})
			pend = vseg{newx, cut + 1, pend.r2}
			if pend.r1 > pend.r2 {
				return vs, hs
			}
		}
	}

	exitSegs, hs := detour(vseg{exit, fy + 1, band})
	lastExit := exit
	if len(exitSegs) > 0 {
		lastExit = exitSegs[len(exitSegs)-1].col
	}
	var finalV []vseg
	finalV = append(finalV, exitSegs...)
	if band < arrowRow {
		if lastExit != tx {
			hs = append(hs, hseg{band, lastExit, tx})
		}
		txSegs, ths := detour(vseg{tx, band + 1, arrowRow})
		hs = append(hs, ths...)
		finalV = append(finalV, txSegs...)
	} else if lastExit != tx {
		hs = append(hs, hseg{band, lastExit, tx})
		finalV = append(finalV, vseg{tx, arrowRow, arrowRow})
	}
	// 统一收尾：保证最后一段竖线以 tx 列结束（箭头位置）
	if lv := len(finalV); lv > 0 {
		last := finalV[lv-1]
		if last.col != tx && last.r1 <= last.r2 {
			rr := last.r2
			if rr > arrowRow-1 {
				rr = arrowRow - 1
			}
			if rr >= last.r1 {
				finalV[lv-1] = vseg{last.col, last.r1, rr}
				hs = append(hs, hseg{rr, last.col, tx})
				if rr < arrowRow {
					finalV = append(finalV, vseg{tx, rr + 1, arrowRow})
				}
			}
		}
	}
	// 绘制：先水平段后竖线（竖线覆盖交叉点，保持垂直贯通）
	for _, h := range hs {
		drawEH(h.row, h.c1, h.c2, false)
	}
	for _, v := range finalV {
		for r := v.r1; r <= v.r2; r++ {
			drawEV(r, v.col)
		}
	}
	// 箭头
	switch {
	case e.xArrow:
		c.set(arrowRow, tx, 'x', c.edgeFg)
	case e.oArrow:
		c.set(arrowRow, tx, 'o', c.edgeFg)
	case e.noArrow:
		c.set(arrowRow, tx, '|', c.edgeFg)
	default:
		c.set(arrowRow, tx, 'v', c.edgeFg)
	}
	// 标签（画在 band 行水平段中间；直线边画在竖线右侧）
	if e.label != "" {
		if exit == tx {
			c.setString(band, tx+1, " "+e.label+" ", c.labFg)
		} else {
			mid := (exit + tx) / 2
			c.setString(band, mid-len([]rune(e.label))/2-1, " "+e.label+" ", c.labFg)
		}
	}
}

// layerGroupTitle 返回层内首个 subgraph 标题（有 group 成员即显示，宽松判断）。
func layerGroupTitle(g *mermaidGraph, nodes []int) string {
	var found string
	for _, ni := range nodes {
		for _, gg := range g.groups {
			if gg.nodes[ni] {
				if found == "" {
					found = gg.title
				}
				break
			}
		}
	}
	return found
}

// layoutHorizontal：LR 布局。每条链横向排列，多条链组成一行，多行上下堆叠；
// 链之间不连线（仅同链节点用箭头相连）。
func layoutHorizontal(g *mermaidGraph, availWidth int) []string {
	chains := buildChains(g)
	if len(chains) == 0 {
		return nil
	}
	n := len(g.nodes)
	nodeW := make([]int, n)
	nodeH := make([]int, n)
	for i, nd := range g.nodes {
		nodeW[i], nodeH[i] = nodeBox(nd, false)
	}
	const gap = 6
	type row struct {
		chains [][]int
	}
	var rows []*row
	var cur *row
	cw := 0
	for _, ch := range chains {
		w := 0
		for _, ni := range ch {
			w += nodeW[ni] + gap
		}
		if cur != nil && cw+w > availWidth-2 {
			rows = append(rows, cur)
			cur = nil
			cw = 0
		}
		if cur == nil {
			cur = &row{}
		}
		cur.chains = append(cur.chains, ch)
		cw += w
	}
	if cur != nil {
		rows = append(rows, cur)
	}
	totalH := 0
	for _, r := range rows {
		mh := 0
		for _, ch := range r.chains {
			for _, ni := range ch {
				if nodeH[ni] > mh {
					mh = nodeH[ni]
				}
			}
		}
		totalH += mh + 2
	}
	canvas := newMermaidCanvas(totalH, availWidth)
	y := 0
	for _, r := range rows {
		x := 1
		for _, ch := range r.chains {
			// 链内节点横向排列
			for k, ni := range ch {
				drawNode(canvas, g.nodes[ni], nodeW[ni], nodeH[ni], x, y)
				if k+1 < len(ch) {
					nj := ch[k+1]
					e := findEdgeBetween(g, ni, nj)
					drawHEdge(canvas, x+nodeW[ni]+1, x+nodeW[ni]+gap-1,
						y+nodeH[ni]/2, e)
				}
				x += nodeW[ni] + gap
			}
			// 链间空隙（gap*2/3）
			x += gap / 2
		}
		mh := 0
		for _, ch := range r.chains {
			for _, ni := range ch {
				if nodeH[ni] > mh {
					mh = nodeH[ni]
				}
			}
		}
		y += mh + 3
	}
	return canvasLines(canvas)
}

// drawHEdge 绘制水平边。
func drawHEdge(c *mermaidCanvas, x1, x2, y int, e *mermaidEdge) {
	if x2 <= x1 {
		return
	}
	mid := (x1 + x2) / 2
	ch := '-'
	if e != nil && e.thick {
		ch = '='
	}
	dashed := e != nil && e.dashed
	for x := x1; x <= x2-1; x++ {
		if x == mid && e != nil && e.label != "" {
			continue
		}
		if dashed {
			if (x-x1)%3 == 0 {
				c.set(y, x, '.', c.edgeFg)
			}
		} else {
			c.set(y, x, ch, c.edgeFg)
		}
	}
	// 箭头（在 x2 位置，紧贴目标节点）
	switch {
	case e != nil && e.xArrow:
		c.set(y, x2, 'x', c.edgeFg)
	case e != nil && e.oArrow:
		c.set(y, x2, 'o', c.edgeFg)
	case e != nil && e.noArrow:
	default:
		c.set(y, x2, '>', c.edgeFg)
	}
	if e != nil && e.label != "" {
		label := " " + e.label + " "
		tw := runewidth.StringWidth(label)
		start := mid - tw/2
		if start < x1+1 {
			start = x1 + 1
		}
		c.setString(y, start, label, c.labFg)
	}
}

// ============================================================
// sequenceDiagram 解析
// ============================================================

func parseSequence(lines []string) *mermaidSeq {
	s := &mermaidSeq{}
	actorIdx := make(map[string]int)
	addActor := func(id, disp string) int {
		if i, ok := actorIdx[id]; ok {
			return i
		}
		actorIdx[id] = len(s.actors)
		s.actorIDs = append(s.actorIDs, id)
		s.actors = append(s.actors, disp)
		return len(s.actors) - 1
	}
	// participant 解析：`participant Client as 客户端` → (id=Client, 显示名=客户端)
	parseParticipant := func(t string) (string, string) {
		rest := strings.TrimSpace(t)
		for _, pref := range []string{"participant ", "actor "} {
			if len(rest) > len(pref) && strings.EqualFold(rest[:len(pref)], pref) {
				rest = strings.TrimSpace(rest[len(pref):])
				break
			}
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return "", ""
		}
		id := f[0]
		disp := id
		if len(f) >= 3 && strings.EqualFold(f[1], "as") {
			disp = strings.Trim(strings.Join(f[2:], " "), "\"'")
		}
		return id, disp
	}
	// 预扫描参与者（保序）
	for _, l := range lines[1:] {
		t := strings.TrimSpace(l)
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "participant ") || strings.HasPrefix(low, "actor ") {
			if id, disp := parseParticipant(t); id != "" {
				addActor(id, disp)
			}
		}
	}
	var open []*seqGroup
	for _, l := range lines[1:] {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "autonumber"):
			s.autoNum = true
		case strings.HasPrefix(low, "participant ") || strings.HasPrefix(low, "actor "):
			// 已预扫描
		case strings.HasPrefix(low, "note "):
			if n := parseNote(t, actorIdx); n != nil {
				s.notes = append(s.notes, n)
			}
		case strings.HasPrefix(low, "loop ") || strings.HasPrefix(low, "alt ") ||
			strings.HasPrefix(low, "opt ") || strings.HasPrefix(low, "par "):
			f := strings.Fields(t)
			kind := strings.ToLower(f[0])
			label := strings.TrimSpace(strings.TrimPrefix(t, f[0]))
			open = append(open, &seqGroup{kind: kind, label: label, start: len(s.messages), elseAt: -1})
		case low == "else" || strings.HasPrefix(low, "else "):
			if len(open) > 0 {
				g := open[len(open)-1]
				g.elseAt = len(s.messages) // 从下一条消息起进入 else 分支
				f := strings.Fields(t)
				if len(f) >= 2 {
					g.elseLabel = strings.TrimSpace(strings.TrimPrefix(t, f[0]))
				}
			}
		case low == "end":
			if len(open) > 0 {
				g := open[len(open)-1]
				open = open[:len(open)-1]
				g.end = len(s.messages) - 1
				s.groups = append(s.groups, g)
			}
		case strings.HasPrefix(low, "activate ") || strings.HasPrefix(low, "deactivate "):
			// 忽略
		default:
			if m := parseSeqMessage(t); m != nil {
				s.messages = append(s.messages, &seqMsg{
					from: addActor(m.from, m.from), to: addActor(m.to, m.to),
					text: m.text, solid: m.solid, arrow: m.arrow,
				})
			}
		}
	}
	if len(s.actors) == 0 && len(s.messages) == 0 {
		return nil
	}
	return s
}

func parseNote(t string, actorIdx map[string]int) *seqNote {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "Note"))
	low := strings.ToLower(rest)
	n := &seqNote{}
	find := func(name string) int {
		if i, ok := actorIdx[name]; ok {
			return i
		}
		return -1
	}
	switch {
	case strings.HasPrefix(low, "left of "):
		n.pos = find(strings.TrimSpace(rest[len("left of "):]))
	case strings.HasPrefix(low, "right of "):
		i := find(strings.TrimSpace(rest[len("right of "):]))
		n.pos = -i - 1
	case strings.HasPrefix(low, "over "):
		n.over = true
		rest2 := strings.TrimSpace(rest[len("over "):])
		idx := strings.Index(rest2, ":")
		if idx < 0 {
			return nil
		}
		names := strings.Split(rest2[:idx], ",")
		if len(names) > 0 {
			n.pos = find(strings.TrimSpace(names[0]))
		}
		n.text = strings.TrimSpace(rest2[idx+1:])
		return n
	default:
		return nil
	}
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return nil
	}
	n.text = strings.TrimSpace(rest[idx+1:])
	return n
}

// seqArrowPatterns 消息箭头模式（长优先）。
var seqArrowPatterns = []struct {
	p     string
	solid bool
	arrow string
}{
	{"-->>", false, "->>"},
	{"-->", false, "->"},
	{"--)", false, "->)"},
	{"--x", false, "->x"},
	{"->>", true, "->>"},
	{"->", true, "->"},
	{"-)", true, "->)"},
	{"-x", true, "->x"},
	{"<-", true, "<-"},
}

type arrowInfo struct {
	solid bool
	arrow string
}

func parseSeqMessage(t string) *seqMsgRaw {
	sep := strings.Index(t, ":")
	if sep < 0 {
		return nil
	}
	head := strings.TrimSpace(t[:sep])
	text := strings.TrimSpace(t[sep+1:])
	bestIdx := -1
	bestEnd := 0
	var best arrowInfo
	for _, p := range seqArrowPatterns {
		if i := strings.Index(head, p.p); i >= 0 && (bestIdx < 0 || i < bestIdx) {
			bestIdx = i
			bestEnd = i + len(p.p)
			best = arrowInfo{solid: p.solid, arrow: p.arrow}
		}
	}
	if bestIdx < 0 {
		return nil
	}
	from := strings.TrimSpace(head[:bestIdx])
	to := strings.TrimSpace(head[bestEnd:])
	if from == "" || to == "" {
		return nil
	}
	return &seqMsgRaw{
		from: strings.Trim(from, "\"'"), to: strings.Trim(to, "\"'"),
		text: text, solid: best.solid, arrow: best.arrow,
	}
}

// ============================================================
// sequenceDiagram 渲染
// ============================================================

func renderSequence(s *mermaidSeq, availWidth int) []string {
	if s == nil || len(s.actors) == 0 {
		return nil
	}
	n := len(s.actors)
	colW := make([]int, n)
	for i, a := range s.actors {
		w := runewidth.StringWidth(a) + 4
		if w < 10 { // 最小 10 列，保证 4 汉字(8 宽)消息不截断
			w = 10
		}
		colW[i] = w
	}
	totalW := 0
	for _, w := range colW {
		totalW += w
	}
	if totalW > availWidth-2 {
		shrink := totalW - (availWidth - 2)
		for shrink > 0 {
			changed := false
			for i := range colW {
				if colW[i] > 10 && shrink > 0 {
					colW[i]--
					shrink--
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}
	// 行数估计（自调用消息占 3 行；每个分组最多 3 个标签行 alt/else/end）
	rowCount := 3
	rowCount += len(s.messages) * 3
	rowCount += len(s.notes) * 2
	rowCount += len(s.groups) * 3
	rowCount += 2
	if rowCount < 8 {
		rowCount = 8
	}
	c := newMermaidCanvas(rowCount, availWidth)
	// 生命线列（列中心）
	lifelines := make([]int, n)
	x := 1
	for i, a := range s.actors {
		lifelines[i] = x + colW[i]/2
		// 参与者名居中
		tw := runewidth.StringWidth(a)
		c.setString(0, x+(colW[i]-tw)/2, a, c.nodeFg)
		x += colW[i]
	}
	// 参与者框（盒子总宽 colW-2，列间距留 2）
	for i, lx := range lifelines {
		w := colW[i] - 2
		left := lx - w/2
		c.set(1, left, '+', c.nodeFg)
		c.hline(1, left+1, left+w-2, '-', c.nodeFg)
		c.set(1, left+w-1, '+', c.nodeFg)
	}
	// 生命线
	for _, lx := range lifelines {
		for r := 2; r < rowCount-1; r++ {
			c.set(r, lx, '|', c.nodeFg)
		}
	}
	// 分组事件（start/else/end），按消息索引排序触发：
	// start 在消息 g.start 前、else 在消息 g.elseAt 前、end 在消息 g.end+1 前
	type seqEv struct {
		at   int // 在渲染消息 at 之前触发；at==len(messages) 表示所有消息之后
		kind int // 0=start, 1=else, 2=end
		g    *seqGroup
	}
	var evs []seqEv
	for _, g := range s.groups {
		evs = append(evs, seqEv{at: g.start, kind: 0, g: g})
		if g.elseAt >= 0 && g.elseAt <= len(s.messages) {
			evs = append(evs, seqEv{at: g.elseAt, kind: 1, g: g})
		}
		evs = append(evs, seqEv{at: g.end + 1, kind: 2, g: g})
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].at != evs[j].at {
			return evs[i].at < evs[j].at
		}
		return evs[i].kind < evs[j].kind
	})
	grpTag := func(kind, label string) string {
		if label != "" {
			return kind + " [" + label + "]"
		}
		return kind
	}
	grpRows := make(map[*seqGroup][2]int) // [起始标签行, end 行]，用于画括号竖线
	y := 2
	ei := 0
	for mi := 0; mi <= len(s.messages); mi++ {
		for ei < len(evs) && evs[ei].at == mi {
			ev := evs[ei]
			switch ev.kind {
			case 0:
				if y < c.h {
					c.setString(y, 1, grpTag(ev.g.kind, ev.g.label), mermaidGroupCol)
				}
				grpRows[ev.g] = [2]int{y, y}
				y++
			case 1:
				if y < c.h {
					c.setString(y, 1, grpTag("else", ev.g.elseLabel), mermaidGroupCol)
				}
				y++
			case 2:
				if y < c.h {
					c.setString(y, 1, "end", mermaidGroupCol)
				}
				if r, ok := grpRows[ev.g]; ok {
					grpRows[ev.g] = [2]int{r[0], y}
				}
				y++
			}
			ei++
		}
		if mi == len(s.messages) {
			break
		}
		m := s.messages[mi]
		x1, x2 := lifelines[m.from], lifelines[m.to]
		txt := m.text
		if s.autoNum {
			txt = itoa(mi+1) + " " + txt
		}
		if x1 == x2 {
			// 自调用（A->>A）：右侧 U 形绕回，占 3 行
			lx := x1
			r := lx + 3
			if r >= c.w-1 {
				r = c.w - 2
			}
			if r <= lx+1 {
				r = lx + 2
			}
			for col := lx + 1; col <= r; col++ {
				c.set(y, col, '-', c.edgeFg)
			}
			c.set(y+1, r, '|', c.edgeFg)
			c.set(y+2, r, '|', c.edgeFg)
			for col := lx + 1; col <= r; col++ {
				c.set(y+2, col, '-', c.edgeFg)
			}
			c.set(y+2, lx+1, '<', c.edgeFg) // 回程箭头指向自己
			// 文本：优先放 U 形右侧（到下一生命线为止）；右侧放不下时若左侧
			// 空间充足则放左侧，否则放右侧并截断到可用宽度
			tw := runewidth.StringWidth(txt)
			limit := c.w - 1
			for _, ll := range lifelines {
				if ll > lx && ll < limit {
					limit = ll
				}
			}
			tx := r + 2
			maxTw := limit - tx - 1
			if tw > maxTw {
				left := 1
				for _, ll := range lifelines {
					if ll < lx && ll > left {
						left = ll
					}
				}
				if lw := lx - left - 2; lw >= tw {
					tx = lx - tw - 1 // 左侧空间足够
				} else if lw > maxTw {
					tx = left + 1 // 左侧更宽：放左侧并截断
					txt = truncateRunes(txt, lw)
					tw = runewidth.StringWidth(txt)
				} else {
					txt = truncateRunes(txt, maxTw) // 都窄：右侧截断
					tw = runewidth.StringWidth(txt)
				}
			}
			c.setString(y, tx, txt, c.labFg)
			y += 3
			continue
		}
		// 普通消息：线 + 箭头（y 行），文本（y+1 行，居中于空隙）
		drawSeqLine(c, y, x1, x2, m.solid, m.arrow)
		lo, hi := x1, x2
		if lo > hi {
			lo, hi = hi, lo
		}
		maxTw := hi - lo - 1
		tw := runewidth.StringWidth(txt)
		if tw > maxTw {
			txt = truncateRunes(txt, maxTw)
			tw = runewidth.StringWidth(txt)
		}
		textX := lo + (hi-lo+1)/2 - tw/2
		if textX < 0 {
			textX = 0
		}
		if y+1 < c.h {
			c.setString(y+1, textX, txt, c.labFg)
		}
		y += 2
	}
	// 分组括号竖线：col 0 贯穿整个分组区间（alt/else/end 标签在 col 1，互不冲突）
	for _, rows := range grpRows {
		for r := rows[0]; r <= rows[1] && r < c.h; r++ {
			if c.runes[r][0] == ' ' {
				c.set(r, 0, '|', mermaidGroupCol)
			}
		}
	}
	// Note
	for _, nt := range s.notes {
		if y >= c.h {
			break
		}
		txt := "[note] " + nt.text
		if nt.over {
			txt = "Note over: " + nt.text
		}
		c.setString(y, 1, txt, mermaidGroupCol)
		y++
	}
	// 底部收尾
	for _, lx := range lifelines {
		if y < c.h {
			c.set(y, lx, 'v', c.edgeFg)
		}
	}
	// 截断画布高度到内容末尾，去掉多余空白行
	if y+1 < c.h {
		c.h = y + 1
	}
	return canvasLines(c)
}

// drawSeqLine 画时序消息线（先画线，文本后写覆盖）。
func drawSeqLine(c *mermaidCanvas, y, x1, x2 int, solid bool, arrow string) {
	if x1 < 0 || x2 < 0 {
		return
	}
	lo, hi := x1, x2
	if lo > hi {
		lo, hi = hi, lo
	}
	for x := lo + 1; x <= hi-1; x++ {
		if c.runes[y][x] != ' ' {
			continue // 生命线位置不覆盖
		}
		if solid {
			c.runes[y][x] = '-'
			c.fg[y][x] = c.edgeFg
		} else if (x-lo)%3 == 0 {
			c.runes[y][x] = '-'
			c.fg[y][x] = c.edgeFg
		}
	}
	// 箭头（画在目标生命线右侧或左侧）
	switch arrow {
	case "->>":
		if x1 < x2 {
			c.set(y, x2, '>', c.edgeFg)
			c.set(y, x2+1, '>', c.edgeFg)
		} else {
			c.set(y, x1, '>', c.edgeFg)
			c.set(y, x1+1, '>', c.edgeFg)
		}
	case "->":
		if x1 < x2 {
			c.set(y, x2, '>', c.edgeFg)
		} else {
			c.set(y, x1, '>', c.edgeFg)
		}
	case "->)":
		if x1 < x2 {
			c.set(y, x2, ')', c.edgeFg)
		} else {
			c.set(y, x1, ')', c.edgeFg)
		}
	case "->x":
		if x1 < x2 {
			c.set(y, x2, 'x', c.edgeFg)
		} else {
			c.set(y, x1, 'x', c.edgeFg)
		}
	case "<-":
		if x1 < x2 {
			c.set(y, x2, '<', c.edgeFg)
		} else {
			c.set(y, x1, '<', c.edgeFg)
		}
	}
}

func truncateRunes(s string, w int) string {
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if cur+rw > w {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
