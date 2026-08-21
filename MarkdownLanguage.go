package termd

import (
	"os"
	"strings"
	"sync"
)

// ============================================================
// MarkdownLanguage.go —— Markdown 语法教程
// ============================================================
//
// RenderMarkdownLanguage 生成 termd 支持的全部 Markdown 语法的教程文本，
// 供两处使用：
//   - `:ml` 命令：在编辑器内打开语法教程视图（Esc 关闭，j/k 或滚轮翻页）；
//   - `termd -ml` 参数：在终端直接打印本教程后退出。
//
// 教程按「写法示例 + 说明」组织：写法示例保持 Markdown 原文（便于对照学习），
// 说明文案经 T() 国际化（zh 原文为 key，en 命中译文，未命中回退中文）。
//
// 配色约定（均为 256 色 / SGR，非 TTY 或 NO_COLOR 时自动降级为纯文本）：
//   - 顶部标题、分节标题【xxx】：亮青色加粗
//   - 写法示例行（Markdown 原文）：亮黄色
//   - 说明文字：默认颜色（不染色，减少视觉干扰）
//   - ═══ 分隔线：暗灰色

// RenderMarkdownLanguage 返回完整的 Markdown 语法教程文本。
func RenderMarkdownLanguage() string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteString("\n") }
	blank := func() { b.WriteString("\n") }

	line(mlWrap("1;36", T("termd 支持的 Markdown 语法教程（Esc 关闭本视图，j/k 或滚轮翻页）")))
	b.WriteString(mlWrap("38;5;240", "═══════════════════════════════════════════════════════════════"))
	blank()
	b.WriteString(T("本教程覆盖 termd 当前支持的全部语法。每个条目先给「写法示例」，再给出说明；"))
	b.WriteString(T("在 Preview 模式中可直接对照渲染效果。"))
	blank()

	// ---- 标题 ----
	section(&b, T("标题"))
	b.WriteString(mlWrap("1;33", "  # 一级标题      ## 二级标题      ### 三级标题"))
	blank()
	b.WriteString(mlWrap("1;33", "  #### 四级标题   ##### 五级标题   ###### 六级标题"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("# 后需跟空格，共 1~6 级；大纲侧边栏（ctrl+t）会收集标题用于跳转。"))
	blank()

	// ---- 段落与换行 ----
	section(&b, T("段落与换行"))
	b.WriteString("  ")
	b.WriteString(T("普通文字组成段落，段与段之间空一行。"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("行尾输入两个空格后回车 → 软换行（仍属同一段落）；直接回车 → 新段落。"))
	blank()

	// ---- 强调 ----
	section(&b, T("强调"))
	b.WriteString(mlWrap("1;33", "  **粗体**              *斜体*                ***粗斜体***"))
	blank()
	b.WriteString(mlWrap("1;33", "  ~~删除线~~            --删除线--（等价写法，内容非空）"))
	blank()
	b.WriteString(mlWrap("1;33", "  ==高亮文字==          %%注释%%（暗灰斜体，仅写作提示，不参与渲染）"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("粗体/斜体/粗斜体额外叠加对比色（亮黄/淡紫/橙红），终端无字形时也一眼可辨。"))
	blank()

	// ---- 行内代码 ----
	section(&b, T("行内代码"))
	b.WriteString(mlWrap("1;33", "  `代码片段`      反引号包裹，灰底绿字"))
	blank()
	b.WriteString(mlWrap("1;33", "  `` 含反引号 `  ``       双反引号可包裹含单个反引号的内容"))
	blank()

	// ---- 代码块 ----
	section(&b, T("代码块"))
	b.WriteString(mlWrap("1;33", "  ```go"))
	blank()
	b.WriteString(mlWrap("1;33", "  func main() { fmt.Println(\"hi\") }"))
	blank()
	b.WriteString(mlWrap("1;33", "  ```"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("以 ``` 或 ~~~ 开头/结束，首行可指定语言名（go/js/python/...）启用语法高亮。"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("语言名写 mermaid 时触发 Mermaid 图形渲染（见末节）。"))
	blank()

	// ---- 链接与图片 ----
	section(&b, T("链接与图片"))
	b.WriteString(mlWrap("1;33", "  [链接文字](https://example.com)      下划线链接，Preview 点击打开"))
	blank()
	b.WriteString(mlWrap("1;33", "  ![图片说明](图片地址)                渲染为 🖼 图片说明 (图片地址)"))
	blank()
	b.WriteString(mlWrap("1;33", "  [目录跳转](#章节标题)               #开头 = 本文件标题锚点（TOC 目录跳转）"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("跨文件目录跳转： [文字](doc.md#标题锚点)；锚点支持标题与 <a id=\"锚点\"> 两种形式。"))
	blank()

	// ---- 列表 ----
	section(&b, T("列表"))
	b.WriteString(mlWrap("1;33", "  - 无序列表      * 无序列表      + 无序列表"))
	blank()
	b.WriteString(mlWrap("1;33", "  1. 有序列表     2. 有序列表     3. 有序列表"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("嵌套列表：缩进 2 个空格表示一级（• → ◦ → ▪ 轮换标记）。"))
	blank()
	b.WriteString(mlWrap("1;33", "  - [ ] 未完成任务        - [x] 已完成任务"))
	blank()

	// ---- 引用 ----
	section(&b, T("引用"))
	b.WriteString(mlWrap("1;33", "  > 引用文字"))
	blank()
	b.WriteString(mlWrap("1;33", "  >> 嵌套引用（> 越多层级越深，左侧用 │ 标记）"))
	blank()

	// ---- 表格 ----
	section(&b, T("表格"))
	b.WriteString(mlWrap("1;33", "  | 列1 | 列2 |"))
	blank()
	b.WriteString(mlWrap("1;33", "  | --- | --- |"))
	blank()
	b.WriteString(mlWrap("1;33", "  | 内容 | 内容 |"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("对齐控制：|:--| 左对齐  |:--:| 居中  |--:| 右对齐。"))
	blank()

	// ---- 分隔线 ----
	section(&b, T("分隔线"))
	b.WriteString(mlWrap("1;33", "  ---     ***     ___     （三个及以上相同符号即可）"))
	blank()

	// ---- 脚注 ----
	section(&b, T("脚注"))
	b.WriteString(mlWrap("1;33", "  正文引用：[^1]        （渲染为绿色上标 ^1）"))
	blank()
	b.WriteString(mlWrap("1;33", "  行内脚注：^[脚注内容]   （渲染为 ※脚注内容）"))
	blank()
	b.WriteString(mlWrap("1;33", "  定义行：  [^1]: 脚注说明"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("脚注定义可多行（下一行缩进 2 空格续写）。"))
	blank()

	// ---- 定义列表 ----
	section(&b, T("定义列表"))
	b.WriteString(mlWrap("1;33", "  术语"))
	blank()
	b.WriteString(mlWrap("1;33", "  : 定义内容"))
	blank()

	// ---- emoji 简码 ----
	section(&b, T("emoji 简码"))
	b.WriteString(mlWrap("1;33", "  :smile: :rocket: :fire: :joy: :heart: ..."))
	blank()
	b.WriteString("  ")
	b.WriteString(T("冒号包裹的 emoji 简码会被渲染为对应 emoji。"))
	blank()

	// ---- 转义 ----
	section(&b, T("转义字符"))
	b.WriteString(mlWrap("1;33", "  \\* \\` \\_ \\[ \\] \\( \\) \\# \\+ \\- \\. \\! \\> \\~ \\|"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("反斜杠 + 上述特殊字符，可输出字面符号而不触发语法。"))
	blank()

	// ---- 多行注释块 ----
	section(&b, T("多行注释块"))
	b.WriteString(mlWrap("1;33", "  %%"))
	blank()
	b.WriteString(mlWrap("1;33", "  整段注释内容（起始行单独写 %%，结束行单独写 %%）"))
	blank()
	b.WriteString(mlWrap("1;33", "  %%"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("注释块内容以暗灰斜体显示，不参与任何语法解析。"))
	blank()

	// ---- Mermaid 绘图 ----
	section(&b, T("Mermaid 绘图"))
	b.WriteString(mlWrap("1;33", "  ```mermaid"))
	blank()
	b.WriteString(mlWrap("1;33", "  graph TD"))
	blank()
	b.WriteString(mlWrap("1;33", "      A[开始] --> B{判断}"))
	blank()
	b.WriteString(mlWrap("1;33", "      B -- 是 --> C[处理]"))
	blank()
	b.WriteString(mlWrap("1;33", "      C --> D((结束))"))
	blank()
	b.WriteString(mlWrap("1;33", "  ```"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("支持 flowchart（TD/TB/LR）与 sequenceDiagram 两类图。"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("节点形状：[矩形] (圆角) ((圆形)) {菱形} [[六边形]] ([圆柱]) >旗标>"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("连线类型：--> 实线  --- 无箭头  -.-> 虚线  ==> 粗线  --x  --o"))
	blank()
	b.WriteString("  ")
	b.WriteString(T("支持 subgraph 分组、连线标签；时序图支持 participant 别名与 loop/alt/opt 分组。"))
	blank()
	b.WriteString("  ")
	b.WriteString(mlWrap("1;31", T("说明：当前 Mermaid 的渲染能力较为有限，难以支持复杂图表的呈现。这主要归因于 Terminal 和 TTY 环境的特殊底层机制，对复杂渲染造成了实质性限制。")))
	blank()

	b.WriteString(T("输入 :keymap 查看键位帮助，:help 查看命令帮助，i/a 进入编辑模式。"))
	blank()
	return b.String()
}

// section 写一条教程分节标题（【xxx】），供 RenderMarkdownLanguage 使用。
func section(b *strings.Builder, title string) {
	b.WriteString(mlWrap("1;36", "【"+title+"】"))
	b.WriteString("\n")
}

// mlWrap 用 SGR 转义序列给文本上色；mlColorEnabled 为 false（非 TTY / NO_COLOR /
// TERM=dumb）时原样返回纯文本，保证重定向或哑终端下可读。
func mlWrap(sgr, s string) string {
	if !mlColorEnabled() {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

var (
	mlColorOnce sync.Once
	mlColorOn   bool
)

// mlColorEnabled 判断教程是否输出 ANSI 彩色：
// NO_COLOR 未设置、TERM 非 dumb、且 stdout 为字符设备（TTY）时才启用。
func mlColorEnabled() bool {
	mlColorOnce.Do(func() {
		mlColorOn = os.Getenv("NO_COLOR") == "" &&
			os.Getenv("TERM") != "dumb" &&
			func() bool {
				fi, err := os.Stdout.Stat()
				return err == nil && fi.Mode()&os.ModeCharDevice != 0
			}()
	})
	return mlColorOn
}
