package core

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"termd"
)

type stlItemType int

const (
	stlTypeText stlItemType = iota
	stlTypeItem
	stlTypeHighlight
	stlTypeSeparate
	stlTypeTrunc
	stlTypeGroup
	stlTypeClickFunc
	stlTypeLineBreak
)

type stlItem struct {
	itemType   stlItemType
	start      int
	text       string
	minwid     int
	maxwid     int
	leftAlign  bool
	zeroPad    bool
	groupDepth int
	clickFunc  string
	highlight  int
	isFlag     bool
	fillable   bool
}

type stlHighlightRec struct {
	start  int
	userhl int
}

type stlClickRec struct {
	start     int
	funcname  string
	minwid    int
}

type stlParser struct {
	fmt         string
	items       []stlItem
	hltab       []stlHighlightRec
	clicktab    []stlClickRec
	separators  []int
	carryHL     int
	width       int
	fillchar    rune
	wp          *EditorModel
	optName     string
	optScope    int
	output      strings.Builder
	curItem     int
	groupDepth  int
	evalDepth   int
	prevFlag    bool
	prevItem    bool
	rawMinwid   int
}

const (
	STL_FILEPATH    = 'f'
	STL_FULLPATH    = 'F'
	STL_FILENAME    = 't'
	STL_MODIFIED    = 'm'
	STL_MODIFIED2   = 'M'
	STL_READONLY    = 'r'
	STL_READONLY2   = 'R'
	STL_HELP        = 'h'
	STL_HELP2       = 'H'
	STL_PREVIEW     = 'w'
	STL_PREVIEW2    = 'W'
	STL_FILETYPE    = 'y'
	STL_FILETYPE2   = 'Y'
	STL_QUICKFIX    = 'q'
	STL_KEYMAP      = 'k'
	STL_BUFNR       = 'n'
	STL_BYTEVAL     = 'b'
	STL_BYTEVALHEX  = 'B'
	STL_BYTEOFF     = 'o'
	STL_BYTEOFFHEX  = 'O'
	STL_LINENR      = 'l'
	STL_LINES       = 'L'
	STL_COL         = 'c'
	STL_VIRTCOL     = 'v'
	STL_VIRTCOL2    = 'V'
	STL_PERCENT     = 'p'
	STL_VIEWPORTPCT = 'P'
	STL_SHOWCMD     = 'S'
	STL_ARGLIST     = 'a'
	STL_VIM_EXPR    = '{'
	STL_EXPR_REEVAL = '%'
	STL_CLICKFUNC   = '['
	STL_LINEBREAK   = '@'
	STL_SEPARATE    = '='
	STL_TRUNCMARK   = '<'
	STL_USER_HL     = '#'
	STL_TABPAGENR   = 'T'
	STL_TABCLOSENR  = 'X'
	STL_GROUP_START = '('
	STL_GROUP_END   = ')'
	STL_HL_END      = '*'
)

const STL_ALL = "fFtmMhrRwWyYqknbBoOlLcVvPpSae%#<=>@[T X(){}*"

var (
	highlightUser  []lipgloss.Style
	highlightStl   []lipgloss.Style
	highlightStlnc []lipgloss.Style
	highlightStlterm []lipgloss.Style
	highlightStltermnc []lipgloss.Style
)

func initHighlights() {
	highlightUser = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("0")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Background(lipgloss.Color("0")),
	}

	highlightStl = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("238")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("240")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("242")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("244")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("246")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("248")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("250")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("252")),
	}

	highlightStlnc = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("234")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("235")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("236")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("237")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("238")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("239")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("240")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("241")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("242")),
	}

	highlightStlterm = highlightStl
	highlightStltermnc = highlightStlnc
}

func (m *EditorModel) buildStatusLine(fmtStr string, width int, multiLine bool) []string {
	if highlightUser == nil {
		initHighlights()
	}

	parser := &stlParser{
		fmt:      fmtStr,
		width:    width,
		fillchar: ' ',
		wp:       m,
		items:    make([]stlItem, 0, 100),
		hltab:    make([]stlHighlightRec, 0, 100),
		clicktab: make([]stlClickRec, 0, 100),
	}

	if multiLine {
		return parser.parseMultiLine()
	}
	return []string{parser.parseSingleLine()}
}

func (p *stlParser) parseSingleLine() string {
	p.parseFormat()
	p.renderItems()
	return p.output.String()
}

func (p *stlParser) parseMultiLine() []string {
	p.parseFormat()
	lines := p.renderMultiLine()
	return lines
}

func (p *stlParser) parseFormat() {
	s := p.fmt
	p.curItem = 0
	p.groupDepth = 0
	p.evalDepth = 0
	p.prevFlag = true
	p.prevItem = false

	for i := 0; i < len(s); {
		if s[i] != '%' {
			start := i
			for i < len(s) && s[i] != '%' {
				if p.carryHL != 0 && (s[i] == '\n' || s[i] == '\r') {
					break
				}
				i++
			}
			if i > start {
				p.addTextItem(s[start:i])
			}
			continue
		}

		if i+1 >= len(s) {
			break
		}
		i++

		ch := s[i]
		i++

		switch ch {
		case '%':
			p.addTextItem("%")
		case STL_CLICKFUNC:
			p.parseClickFunc(s, &i)
		case STL_LINEBREAK:
			p.addLineBreak()
		case STL_SEPARATE:
			p.addSeparate()
		case STL_TRUNCMARK:
			p.addTruncMark()
		case STL_USER_HL:
			p.parseHighlight(s, &i)
		case STL_TABPAGENR, STL_TABCLOSENR:
			p.parseTabPage(ch, s, &i)
		case STL_GROUP_START:
			p.parseGroupStart(s, &i)
		case STL_GROUP_END:
			p.parseGroupEnd()
		case STL_HL_END:
			p.addHighlight(0)
		case STL_VIM_EXPR:
			p.parseVimExpr(s, &i)
		default:
			if strings.ContainsRune(STL_ALL, rune(ch)) {
				p.parseStatusItem(ch, s, &i)
			}
		}
	}

	if p.carryHL != 0 {
		p.addHighlight(p.carryHL)
	}
}

func (p *stlParser) addTextItem(text string) {
	if len(text) == 0 {
		return
	}
	p.items = append(p.items, stlItem{
		itemType: stlTypeText,
		text:     text,
		fillable: false,
	})
	p.prevFlag = false
	p.prevItem = false
}

func (p *stlParser) parseClickFunc(s string, i *int) {
	if *i < len(s) && s[*i] == ']' {
		p.items = append(p.items, stlItem{
			itemType:  stlTypeClickFunc,
			clickFunc: "",
		})
		*i++
		return
	}

	start := *i
	for *i < len(s) && (isAlphaNum(s[*i]) || s[*i] == '_') {
		*i++
	}
	if *i < len(s) && s[*i] == ']' {
		funcName := s[start:*i]
		p.items = append(p.items, stlItem{
			itemType:  stlTypeClickFunc,
			clickFunc: funcName,
			minwid:    p.rawMinwid,
		})
		*i++
	}
	p.rawMinwid = 0
}

func (p *stlParser) addLineBreak() {
	p.items = append(p.items, stlItem{
		itemType: stlTypeLineBreak,
	})
}

func (p *stlParser) addSeparate() {
	if p.groupDepth > 0 {
		return
	}
	p.items = append(p.items, stlItem{
		itemType: stlTypeSeparate,
	})
	p.separators = append(p.separators, len(p.items)-1)
}

func (p *stlParser) addTruncMark() {
	p.items = append(p.items, stlItem{
		itemType: stlTypeTrunc,
	})
}

func (p *stlParser) parseHighlight(s string, i *int) {
	if *i >= len(s) {
		return
	}

	if s[*i] == STL_HL_END {
		p.addHighlight(0)
		*i++
		return
	}

	end := strings.IndexByte(s[*i:], '#')
	if end == -1 {
		return
	}
	hlName := s[*i : *i+end]
	*i += end + 1

	hlID := p.getHighlightID(hlName)
	p.addHighlight(hlID)
}

func (p *stlParser) addHighlight(hlID int) {
	p.items = append(p.items, stlItem{
		itemType:  stlTypeHighlight,
		highlight: hlID,
	})
}

func (p *stlParser) getHighlightID(name string) int {
	if name == "" || name == "*" || name == "0" {
		return 0
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 1 && n <= 9 {
		return n
	}
	for i := range highlightUser {
		return i + 1
	}
	return 1
}

func (p *stlParser) parseTabPage(ch byte, s string, i *int) {
	minwid := 0
	if *i < len(s) && isDigit(s[*i]) {
		minwid = 0
		for *i < len(s) && isDigit(s[*i]) {
			minwid = minwid*10 + int(s[*i]-'0')
			*i++
		}
	}
	if ch == STL_TABCLOSENR {
		if minwid == 0 {
			for n := len(p.items) - 1; n >= 0; n-- {
				if p.items[n].itemType == stlTypeItem && p.items[n].text == "T" {
					minwid = p.items[n].minwid
					break
				}
			}
		} else {
			minwid = -minwid
		}
	}
	p.items = append(p.items, stlItem{
		itemType: stlTypeItem,
		text:     "T",
		minwid:   minwid,
	})
	p.rawMinwid = 0
}

func (p *stlParser) parseGroupStart(s string, i *int) {
	p.groupDepth++
	minwid := p.rawMinwid
	maxwid := 9999
	zeropad := false
	leftAlign := false

	if minwid < 0 {
		leftAlign = true
		minwid = -minwid
	}

	p.items = append(p.items, stlItem{
		itemType:   stlTypeGroup,
		minwid:     minwid,
		maxwid:     maxwid,
		leftAlign:  leftAlign,
		zeroPad:    zeropad,
		groupDepth: p.groupDepth,
	})
	p.rawMinwid = 0
}

func (p *stlParser) parseGroupEnd() {
	if p.groupDepth < 1 {
		return
	}
	p.groupDepth--
}

func (p *stlParser) parseVimExpr(s string, i *int) {
	reeval := false
	if *i < len(s) && s[*i] == STL_EXPR_REEVAL {
		reeval = true
		*i++
	}

	start := *i
	depth := 1
	for *i < len(s) && depth > 0 {
		if s[*i] == '}' && (!reeval || (*i > 0 && s[*i-1] != '%')) {
			depth--
		} else if s[*i] == '{' && reeval {
			depth++
		}
		if depth > 0 {
			*i++
		}
	}
	if *i >= len(s) {
		return
	}
	expr := s[start:*i]
	*i++

	p.items = append(p.items, stlItem{
		itemType:  stlTypeItem,
		text:      expr,
		minwid:    p.rawMinwid,
		highlight: -1,
	})
	p.rawMinwid = 0
}

func (p *stlParser) parseStatusItem(ch byte, s string, i *int) {
	minwid := 0
	maxwid := 9999
	zeropad := false
	leftAlign := false
	l := 1

	if ch == '0' {
		zeropad = true
		if *i < len(s) {
			ch = s[*i]
			*i++
		}
	}
	if ch == '-' {
		leftAlign = true
		l = -1
		if *i < len(s) {
			ch = s[*i]
			*i++
		}
	}
	for *i < len(s) && isDigit(s[*i]) {
		minwid = minwid*10 + int(s[*i]-'0')
		*i++
	}
	if minwid > 50 {
		minwid = 50
	}
	p.rawMinwid = minwid * l
	minwid = minwid * l

	if ch == STL_USER_HL {
		p.addHighlight(minwid)
		return
	}

	if ch == STL_TABPAGENR || ch == STL_TABCLOSENR {
		p.parseTabPage(ch, s, i)
		return
	}

	if *i < len(s) && s[*i] == '.' {
		*i++
		for *i < len(s) && isDigit(s[*i]) {
			maxwid = maxwid*10 + int(s[*i]-'0')
			*i++
		}
		if maxwid <= 0 {
			maxwid = 50
		}
	}

	if *i < len(s) && s[*i] == STL_GROUP_START {
		p.parseGroupStart(s, i)
		return
	}

	itemisflag := false
	fillable := true
	str := ""
	num := int64(-1)

	switch ch {
	case STL_FILEPATH, STL_FULLPATH, STL_FILENAME:
		fillable = false
		name := p.wp.Buf.FilePath()
		if name == "" {
			name = "[无名称]"
		}
		if ch == STL_FILENAME {
			parts := strings.Split(name, "/")
			name = parts[len(parts)-1]
		}
		str = name
	case STL_MODIFIED:
		itemisflag = true
		if p.wp.Buf.IsDirty {
			str = "[+]"
		} else {
			str = "[-]"
		}
	case STL_MODIFIED2:
		itemisflag = true
		if p.wp.Buf.IsDirty {
			str = ",+"
		} else {
			str = ",-"
		}
	case STL_READONLY:
		itemisflag = true
		// Buffer doesn't have ReadOnly field, use a fallback
		str = ""
	case STL_READONLY2:
		itemisflag = true
		str = ""
	case STL_HELP:
		itemisflag = true
		// Check if file is help (by extension or content)
		str = ""
	case STL_HELP2:
		itemisflag = true
		str = ""
	case STL_PREVIEW:
		itemisflag = true
		if p.wp.sm.Mode() == termd.ModePreview {
			str = "[预览]"
		}
	case STL_PREVIEW2:
		itemisflag = true
		if p.wp.sm.Mode() == termd.ModePreview {
			str = ",预览"
		}
	case STL_FILETYPE:
		itemisflag = true
		// Could infer from file extension
		str = ""
	case STL_FILETYPE2:
		itemisflag = true
		str = ""
	case STL_QUICKFIX:
		itemisflag = true
	case STL_KEYMAP:
		itemisflag = true
	case STL_BUFNR:
		// Use buffer ID if available
		num = 1
	case STL_BYTEVAL:
		num = int64(p.wp.cursorByteVal())
	case STL_BYTEVALHEX:
		num = int64(p.wp.cursorByteVal())
		str = fmt.Sprintf("0x%X", num)
	case STL_BYTEOFF:
		num = int64(p.wp.cursorByteOffset())
	case STL_BYTEOFFHEX:
		num = int64(p.wp.cursorByteOffset())
		str = fmt.Sprintf("0x%X", num)
	case STL_LINENR:
		row := p.wp.cursorRow
		if p.wp.sm.Mode() == termd.ModePreview {
			row = p.wp.previewCursor
		}
		num = int64(row + 1)
	case STL_LINES:
		num = int64(p.wp.Buf.LineCount())
	case STL_COL:
		num = int64(p.wp.cursorCol + 1)
	case STL_VIRTCOL:
		num = int64(p.wp.cursorVirtCol() + 1)
	case STL_VIRTCOL2:
		vc := p.wp.cursorVirtCol() + 1
		c := p.wp.cursorCol + 1
		if vc != c {
			str = fmt.Sprintf("-%d", vc)
		}
	case STL_PERCENT:
		row := p.wp.cursorRow
		if p.wp.sm.Mode() == termd.ModePreview {
			row = p.wp.previewCursor
		}
		total := p.wp.Buf.LineCount()
		pct := calcPercentage(row+1, total)
		str = fmt.Sprintf("%d%%", pct)
	case STL_VIEWPORTPCT:
		str = p.wp.viewportProgress()
	case STL_SHOWCMD:
		str = p.wp.sm.CmdInput
	case STL_ARGLIST:
		str = ""
	}

	p.items = append(p.items, stlItem{
		itemType:  stlTypeItem,
		text:      str,
		minwid:    minwid,
		maxwid:    maxwid,
		leftAlign: leftAlign,
		zeroPad:   zeropad,
		isFlag:    itemisflag,
		fillable:  fillable,
	})
	if num >= 0 {
		p.items[len(p.items)-1].text = fmt.Sprintf("%d", num)
	}

	if itemisflag && p.prevFlag && p.prevItem && strings.HasSuffix(p.getPrevText(), ",") {
		p.trimPrevComma()
	}

	p.prevFlag = itemisflag
	p.prevItem = true
}

func (p *stlParser) getPrevText() string {
	for i := len(p.items) - 1; i >= 0; i-- {
		if p.items[i].itemType == stlTypeText || p.items[i].itemType == stlTypeItem {
			return p.items[i].text
		}
	}
	return ""
}

func (p *stlParser) trimPrevComma() {
	for i := len(p.items) - 1; i >= 0; i-- {
		if p.items[i].itemType == stlTypeText || p.items[i].itemType == stlTypeItem {
			p.items[i].text = strings.TrimSuffix(p.items[i].text, ",")
			break
		}
	}
}

func (p *stlParser) renderItems() {
	p.output.Reset()
	p.hltab = p.hltab[:0]
	p.clicktab = p.clicktab[:0]

	curHL := 0
	curClickFunc := ""
	curClickMinwid := 0
	outputPos := 0

	for _, item := range p.items {
		switch item.itemType {
		case stlTypeText:
			p.output.WriteString(item.text)
			outputPos += runewidth.StringWidth(item.text)
		case stlTypeItem:
			text := item.text
			if item.minwid != 0 {
				w := runewidth.StringWidth(text)
				if item.leftAlign {
					for w < item.minwid {
						text += " "
						w++
					}
				} else {
					for w < item.minwid {
						text = " " + text
						w++
					}
				}
			}
			if item.maxwid < 9999 && runewidth.StringWidth(text) > item.maxwid {
				text = runewidth.Truncate(text, item.maxwid, "…")
			}
			p.hltab = append(p.hltab, stlHighlightRec{
				start:  outputPos,
				userhl:  curHL,
			})
			p.output.WriteString(text)
			outputPos += runewidth.StringWidth(text)
		case stlTypeHighlight:
			curHL = item.highlight
		case stlTypeSeparate:
			p.separators = append(p.separators, outputPos)
		case stlTypeTrunc:
		case stlTypeClickFunc:
			curClickFunc = item.clickFunc
			curClickMinwid = item.minwid
			p.clicktab = append(p.clicktab, stlClickRec{
				start:    outputPos,
				funcname: curClickFunc,
				minwid:   curClickMinwid,
			})
		case stlTypeLineBreak:
		}
	}

	p.hltab = append(p.hltab, stlHighlightRec{start: outputPos, userhl: 0})
}

func (p *stlParser) renderMultiLine() []string {
	var lines []string
	lineBuf := strings.Builder{}
	p.hltab = p.hltab[:0]
	p.clicktab = p.clicktab[:0]

	curHL := 0
	outputPos := 0
	lineWidth := 0

	for _, item := range p.items {
		switch item.itemType {
		case stlTypeText:
			lineBuf.WriteString(item.text)
			outputPos += runewidth.StringWidth(item.text)
			lineWidth += runewidth.StringWidth(item.text)
		case stlTypeItem:
			text := item.text
			if item.minwid != 0 {
				w := runewidth.StringWidth(text)
				if item.leftAlign {
					for w < item.minwid {
						text += " "
						w++
					}
				} else {
					for w < item.minwid {
						text = " " + text
						w++
					}
				}
			}
			if item.maxwid < 9999 && runewidth.StringWidth(text) > item.maxwid {
				text = runewidth.Truncate(text, item.maxwid, "…")
			}
			p.hltab = append(p.hltab, stlHighlightRec{
				start:  outputPos,
				userhl:  curHL,
			})
			lineBuf.WriteString(text)
			outputPos += runewidth.StringWidth(text)
			lineWidth += runewidth.StringWidth(text)
		case stlTypeHighlight:
			curHL = item.highlight
		case stlTypeSeparate:
			p.separators = append(p.separators, outputPos)
		case stlTypeLineBreak:
			lines = append(lines, lineBuf.String())
			lineBuf.Reset()
			outputPos = 0
			lineWidth = 0
		}
	}

	if lineBuf.Len() > 0 {
		lines = append(lines, lineBuf.String())
	}

	return lines
}

func (m *EditorModel) cursorByteVal() int {
	line := m.Buf.GetLine(m.cursorRow)
	if m.cursorCol < len(line) {
		return int(line[m.cursorCol])
	}
	return 0
}

func (m *EditorModel) cursorByteOffset() int {
	offset := 0
	for i := 0; i < m.cursorRow; i++ {
		offset += len(m.Buf.GetLine(i)) + 1
	}
	offset += m.cursorCol + 1
	return offset
}

func (m *EditorModel) cursorVirtCol() int {
	line := m.Buf.GetLine(m.cursorRow)
	return runewidth.StringWidth(string(line[:minInt(m.cursorCol, len(line))]))
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *EditorModel) statusBarV2() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	defaultFormat := "%<%f %h%w%m%r%=%-14.(%l,%c%V%) %P"

	lines := m.buildStatusLine(defaultFormat, width, false)
	if len(lines) == 0 {
		return ""
	}

	return m.applyHighlight(lines[0])
}

func (m *EditorModel) applyHighlight(line string) string {
	return line
}

func (m *EditorModel) statusBarEnhanced() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	mode := termd.ModeNames[m.sm.Mode()]
	if sub := m.sm.EditSubName(); sub != "" {
		mode = sub
	}

	mc := m.modeBlockColors()

	fname := m.Buf.FilePath()
	if fname == "" {
		fname = "[无名称]"
	}

	encTag := ""
	if name := m.Buf.Encoding.Name; name != "" {
		encTag = "[" + name + "]"
	}

	flags := ""
	if m.Buf.IsDirty {
		flags += "[+]"
	}

	leftText := fmt.Sprintf(" %s  %s %s %s ", mode, fname, encTag, flags)
	if m.status != "" {
		leftText += m.status + " "
	}

	rowLine := m.cursorRow
	if m.sm.Mode() == termd.ModePreview {
		rowLine = m.previewCursor
	}
	total := m.Buf.LineCount()
	col := m.cursorCol + 1
	_ = m.cursorVirtCol() + 1

	pct := calcPercentage(rowLine+1, total)
	progress := fmt.Sprintf("%d%%", pct)

	lnMode := ""
	if lm := m.sm.LineNumMode(); lm != termd.LNNone {
		lnMode = " " + lm.Name()
	}

	vpProgress := m.viewportProgress()

	rightText := fmt.Sprintf("%d,%d  %s  %dL%s  %s ", rowLine+1, col, progress, total, lnMode, vpProgress)

	leftW := runewidth.StringWidth(leftText)
	rightW := runewidth.StringWidth(rightText)
	pad := width - leftW - rightW
	if pad < 0 {
		pad = 0
	}

	modeStyle := lipgloss.NewStyle().Background(mc.bg).Foreground(mc.fg)
	rulerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("238")).Foreground(lipgloss.Color("252"))

	left := modeStyle.Render(leftText)
	mid := rulerStyle.Render(strings.Repeat(" ", pad))
	right := rulerStyle.Render(rightText)

	return left + mid + right
}