package core

import (
	"strings"
	"termd"
)

// rebuildPreview 构建 Preview 渲染缓存（block-aware + 行号前缀 + 映射表）。
//
// 渲染策略：
//   - 代码块（```...```）、表格（| ... |）、数学块（$$...$$）、脚注定义（[^id]:）
//     等跨行块语法整块送 glamour 渲染（RenderBlock），保证表格对齐/代码高亮等正确。
//   - 普通行逐行 RenderLine（标题/列表/引用/段落），并渲染行内语法（粗体/链接/emoji…）。
//   - 每个 buffer 行对应一个预览块；块内多行渲染结果共享该 buffer 行号（行号列
//     仅首行显示，其余留空），维持与编辑态的 1:1 行号对应。
func (m *EditorModel) rebuildPreview() {
	// 行号列宽：仅在编辑模式（termd.ModeEdit）下渲染行号；预览模式为沉浸式阅读，
	// 不应渲染行号（否则行号前缀会被 markdown 语法误解析/显示错乱）。
	const editLineNumCol = 5 // 与 renderLineNum 的 "%4d " 列宽（5 列）保持一致
	ln := termd.LNNone
	lineNumCol := 0
	if m.sm.Mode() == termd.ModeEdit {
		ln = m.sm.LineNumMode()
		if ln != termd.LNNone {
			lineNumCol = editLineNumCol
		}
	}
	blankPrefix := strings.Repeat(" ", lineNumCol)
	width := m.contentWidth() - lineNumCol
	if width < 20 {
		width = 20
	}
	// 让 renderer 知道可用宽度：分隔线/标题下划线等占满整行
	m.Rend.SetWrap(width)

	// 预览模式光标所在的 buffer 行：若落在代码块/表格范围内，则该块降级为原始文本渲染，
	// 规避块级宽内容（超长代码行/宽表格）被终端硬折行导致的滚动飘逸。仅预览模式生效。
	cursorBuf := -1
	if m.sm.Mode() == termd.ModePreview {
		cursorBuf = m.previewCursor
	}

	lines := m.Buf.Lines
	var outLines []string
	var rowToBuf []int
	var bufToPrev = make([]int, len(lines))

	i := 0
	for i < len(lines) {
		brow := i
		lineStr := string(lines[i])
		trimmed := strings.TrimSpace(lineStr)
		nextLine := ""
		if i+1 < len(lines) {
			nextLine = string(lines[i+1])
		}

		// 行号前缀（仅块首行显示 buffer 行号）
		prefix := renderLineNum(ln, brow, brow)
		if prefix == "" {
			prefix = blankPrefix
		}

		switch {
		case termd.IsFenceStart(trimmed):
			// 代码块：收集到结束 fence（含）
			j := i + 1
			for j < len(lines) && !termd.IsFenceEnd(strings.TrimSpace(string(lines[j]))) {
				j++
			}
			if j >= len(lines) {
				j = len(lines) - 1 // 无结束 fence，渲染到文末
			}
			// 预览模式下，若光标（高亮行）落在代码块范围内，则整块降级为“原始文本逐行渲染”，
			// 不触发 chroma 矩形/块级渲染。块级宽内容（超长代码行、表格对齐）是飘逸的根源，
			// 光标悬停时只看源码文本可彻底规避硬折行错位；光标离开后（rebuildPreview 重建）立即恢复渲染。
			if m.sm.Mode() == termd.ModePreview && cursorBuf >= i && cursorBuf <= j {
				for r := i; r <= j; r++ {
					rp := renderLineNum(ln, r, r)
					if rp == "" {
						rp = blankPrefix
					}
					rl := m.Rend.RenderLine(lines[r], true, m.sm.SearchKeyword)
					rlines := termd.WrapText(rl, width)
					for k, seg := range rlines {
						p := rp
						if k > 0 {
							p = blankPrefix
						}
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, r)
					}
					bufToPrev[r] = len(outLines) - len(rlines)
				}
				i = j + 1
				continue
			}
			rawInner := termd.ByteLinesToStrings(lines[i+1 : j])
			// 代码块不走高亮由 glamour 渲染：其 dark 风格用"灰色前景空格铺满整行"伪装灰底，
			// 无法裁成自适应宽度矩形。改为 chroma 直接高亮（无背景），再由 renderCodeRect
			// 套真正的灰底矩形，宽度 = 原始代码内容宽度。
			fenceStart := strings.TrimSpace(string(lines[i]))
			lang := strings.TrimPrefix(fenceStart, "```")
			lang = strings.TrimPrefix(lang, "~~~")
			code := strings.Join(rawInner, "\n")
			rlines := highlightCode(lang, code)
			rect := renderCodeRect(rlines, rawInner, prefix, blankPrefix, width-contentWidth(prefix)-1)
			baseOut := len(outLines)
			for _, rl := range rect {
				outLines = append(outLines, rl)
				rowToBuf = append(rowToBuf, brow)
			}
			// 用实际追加行数回溯映射（软换行续行归属同一 buffer 行，取块首行）。
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case termd.IsTableStart(trimmed, nextLine):
			// 表格：收集连续的含 | 行
			j := i
			for j+1 < len(lines) && strings.Contains(string(lines[j+1]), "|") && strings.TrimSpace(string(lines[j+1])) != "" {
				j++
			}
			// 预览模式下，若光标落在表格范围内，则整块降级为“原始文本逐行渲染”，
			// 不触发 glamour 表格渲染（超宽表格对齐行被终端硬折行是飘逸根源之一）。
			if m.sm.Mode() == termd.ModePreview && cursorBuf >= i && cursorBuf <= j {
				for r := i; r <= j; r++ {
					rp := renderLineNum(ln, r, r)
					if rp == "" {
						rp = blankPrefix
					}
					rl := m.Rend.RenderLine(lines[r], true, m.sm.SearchKeyword)
					rlines := termd.WrapText(rl, width)
					for k, seg := range rlines {
						p := rp
						if k > 0 {
							p = blankPrefix
						}
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, r)
					}
					bufToPrev[r] = len(outLines) - len(rlines)
				}
				i = j + 1
				continue
			}
			block := strings.Join(termd.ByteLinesToStrings(lines[i:j+1]), "\n")
			rendered := m.Rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				// 按可用宽度软换行，避免超宽表格行被终端硬折行（导致滚动偏移错位/飘逸）。
				for _, seg := range termd.WrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, brow)
				}
			}
			// 用实际追加行数回溯映射（软换行续行也归属同一 buffer 行，故取块首行）。
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case termd.IsMathBlock(trimmed):
			if termd.IsSingleLineMath(trimmed) {
				// 单行显示公式 $$...$$：仅当前行作为块渲染
				block := lineStr
				rendered := m.Rend.RenderBlock(block, width)
				rlines := strings.Split(rendered, "\n")
				aw := width - contentWidth(prefix)
				if aw < 4 {
					aw = 4
				}
				baseOut := len(outLines)
				for k, rl := range rlines {
					p := prefix
					if k > 0 {
						p = blankPrefix
					}
					for _, seg := range termd.WrapText(rl, aw) {
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, brow)
					}
				}
				bufToPrev[brow] = baseOut
				i++
				continue
			}
			// 多行数学块 $$ ... $$
			j := i + 1
			for j < len(lines) && !termd.IsMathBlockDelim(strings.TrimSpace(string(lines[j]))) {
				j++
			}
			if j >= len(lines) {
				j = len(lines) - 1
			}
			block := strings.Join(termd.ByteLinesToStrings(lines[i:j+1]), "\n")
			rendered := m.Rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				for _, seg := range termd.WrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, brow)
				}
			}
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case termd.IsDefListLine(trimmed):
			// 定义列表：含前导术语行 + 后续 : 定义行，整块送 glamour 渲染
			start := i
			if start > 0 {
				prev := strings.TrimSpace(string(lines[start-1]))
				if termd.IsDefTermLine(prev) {
					start--
				}
			}
			j := i
			for j+1 < len(lines) && termd.IsDefListLine(strings.TrimSpace(string(lines[j+1]))) {
				j++
			}
			block := strings.Join(termd.ByteLinesToStrings(lines[start:j+1]), "\n")
			rendered := m.Rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				for _, seg := range termd.WrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, start)
				}
			}
			bufToPrev[start] = baseOut
			i = j + 1
			continue

		case func() bool {
			_, _, ok := termd.ExtractBlockImage(trimmed)
			return ok
		}():
			// 独立成行的图片 ![alt](url)：用 🖼 占位符显示（图形预览已关闭，见下方说明）。
			alt, url, _ := termd.ExtractBlockImage(trimmed)
			note := "🖼 " + alt + " (" + url + ")"
			ph := m.Rend.Styles.Img.Render(note)
			outLines = append(outLines, prefix+ph)
			rowToBuf = append(rowToBuf, brow)
			bufToPrev[brow] = len(outLines) - 1
			i++
			continue

		default:
			// 普通行：逐行富文本渲染（含行内语法），再软换行到可用宽度
			rendered := m.Rend.RenderLine(lines[i], true, m.sm.SearchKeyword)
			rlines := termd.WrapText(rendered, width)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				outLines = append(outLines, p+rl)
				rowToBuf = append(rowToBuf, brow)
			}
			bufToPrev[brow] = len(outLines) - len(rlines)
			i++
		}
	}

	m.previewLines = outLines
	m.previewRowToBuffer = rowToBuf
	m.bufferToPreview = bufToPrev

	// 内容/宽度变化后把视口偏移钳制到新边界（位置尽量保留，收缩到合法范围）
	m.clampPreviewScroll()
}
