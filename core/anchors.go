package core

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"termd"
)

// ============================================================
// anchors —— Markdown 锚点链接跳转（TOC 目录跳转）
// ============================================================
//
// 支持 [text](#anchor)（本文件锚点）与 [text](doc.md#anchor)（其它 md 文件锚点），
// 用于制作目录：写 [章节名](#章节标题) 即可在点击 / 回车时跳转到对应标题行。
//
// 锚点匹配顺序：
//   1. 标题的 GitHub 风格 slug（小写、去标点、空白转连字符），兼容 URL 编码；
//   2. 标题原文精确匹配（中文标题可直接写原文）；
//   3. HTML 锚点行 <a id="x"> / <a name="x">。
//
// 与大纲侧边栏（outline.go）共用 extractOutline 作为标题来源，定位口径一致：
// Edit 模式移动光标行，Preview 模式移动高亮行并保证视口可见。

// htmlAnchorRE 匹配 HTML 锚点行：<a id="foo"> / <a name='foo'>。
var htmlAnchorRE = regexp.MustCompile(`(?i)<a\s+(?:id|name)\s*=\s*["']([^"']+)["']`)

// urlSchemeRE 匹配带 scheme 的 URL（http://, https://, mailto:, file: 等），用于排除非锚点链接。
var urlSchemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// isMarkdownFile 判断路径是否为 Markdown 文件（.md, .markdown, .mdown 等）。
func isMarkdownFile(path string) bool {
	ext := strings.ToLower(path)
	return strings.HasSuffix(ext, ".md") || strings.HasSuffix(ext, ".markdown") || strings.HasSuffix(ext, ".mdown")
}

// githubSlug 按 GitHub 风格把标题文本转成锚点 slug：
// 小写、丢弃标点（保留 - 与 _）、空白转连字符（连续空白合并为一个）。
func githubSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r):
			if space && b.Len() > 0 {
				b.WriteByte('-')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

// anchorLine 返回锚点名 anchor 对应的 buffer 行（0-based）；未命中返回 false。
func (m *EditorModel) anchorLine(anchor string) (int, bool) {
	// URL 解码：%E6%A0%87%E9%A2%98 → 标题；解码失败则用原串。
	if dec, err := url.PathUnescape(anchor); err == nil {
		anchor = dec
	}
	slug := githubSlug(anchor)

	// 1) 标题 slug 匹配（GitHub 风格锚点）
	items := extractOutline(m.Buf)
	for _, it := range items {
		if githubSlug(it.Title) == slug {
			return it.Line, true
		}
	}
	// 2) 标题原文精确匹配（中文标题直接写原文）
	for _, it := range items {
		if it.Title == anchor {
			return it.Line, true
		}
	}
	// 3) HTML 锚点行匹配
	for i, lb := range m.Buf.Lines {
		if mm := htmlAnchorRE.FindStringSubmatch(string(lb)); mm != nil && mm[1] == anchor {
			return i, true
		}
	}
	return 0, false
}

// jumpToAnchor 跳到本文件内锚点 anchor 对应的标题行。
// 与大纲的 syncOutlineToContent 定位口径一致：Edit 模式移动光标行并保证可见，
// Preview 模式移动高亮行并保证视口可见。
func (m *EditorModel) jumpToAnchor(anchor string) {
	line, ok := m.anchorLine(anchor)
	if !ok {
		m.status = termd.Tf("未找到锚点: %s", anchor)
		return
	}
	// Preview 模式定位依赖 bufferToPreview 映射；缓存陈旧（如跨文件打开后）先重建，
	// 与 centerPreviewOnCursor 的惰性重建处理一致。
	if m.sm.Mode() == termd.ModePreview && m.previewDirty {
		m.rebuildPreview()
	}
	if m.sm.Mode() == termd.ModeEdit {
		m.cursorRow = clamp(line, 0, m.Buf.LineCount()-1)
		m.cursorCol = 0
		m.cursWant = 0
		m.ensureCursorVisible()
	} else {
		m.previewCursor = clamp(line, 0, m.Buf.LineCount()-1)
		m.ensurePreviewCursorVisible()
	}
	m.status = termd.Tf("已跳转到锚点: %s", anchor)
}

// splitAnchorTarget 判断链接目标是否为文档内锚点链接，返回（相对路径, 锚点名, 是否锚点链接）：
//   - "#anchor"                → ("", "anchor", true)          当前文档内跳转
//   - "sub/doc.md#anchor"      → ("sub/doc.md", "anchor", true)  其它 md 文档内跳转
//
// http(s) 等带 scheme 的 URL（即使含 #fragment）一律不视为锚点链接，保持原有浏览器打开。
func splitAnchorTarget(target string) (string, string, bool) {
	if strings.HasPrefix(target, "#") {
		if len(target) > 1 {
			return "", target[1:], true
		}
		return "", "", false // 空锚点 "#" 不是跳转
	}
	if urlSchemeRE.MatchString(target) {
		return "", "", false
	}
	if i := strings.IndexByte(target, '#'); i > 0 {
		p, a := target[:i], target[i+1:]
		if isMarkdownFile(p) && a != "" {
			return p, a, true
		}
	}
	return "", "", false
}
