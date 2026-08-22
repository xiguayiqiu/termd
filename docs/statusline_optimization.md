# termd 状态栏深度优化方案

## 概述

基于对 Vim 源码中状态栏实现的深度分析（`vim-master/src/buffer.c` 的 `build_stl_str_hl_local`、`vim-master/src/screen.c` 的 `win_redr_custom`、`vim-master/runtime/doc/options.txt` 的 `statusline` 文档），对 termd 的状态栏进行全面优化，实现 Vim 兼容的状态栏格式语法解析器。

## Vim 状态栏核心架构分析

### 核心数据结构

```c
// vim-master/src/buffer.c:4489
static int build_stl_str_hl_local(
    stl_mode_T mode,           // 单行/多行/获取高度模式
    win_T *wp,                 // 目标窗口
    char_u *out,               // 输出缓冲区
    size_t outlen,             // 缓冲区长度
    char_u **fmt_arg,          // 格式字符串（输入/输出）
    char_u *opt_name,          // 选项名
    int opt_scope,             // 选项作用域
    int fillchar,              // 填充字符
    int maxwidth,              // 最大宽度
    stl_hlrec_T **hltab,       // 高亮属性数组
    stl_hlrec_T **tabtab,      // 标签页号数组
    stl_clickrec_T **clicktab, // 点击区域数组
    int *rendered_height,      // 渲染高度
    int *carry_hl              // 跨行高亮继承
)
```

### 状态栏格式项语法

```
%-0{minwid}.{maxwid}{item}
```

| 字段 | 含义 |
|------|------|
| `%` | 格式项起始 |
| `-` | 左对齐（默认右对齐） |
| `0` | 数字项前导零 |
| `{minwid}` | 最小宽度（≤50） |
| `.{maxwid}` | 最大宽度，超出截断并在左侧显示 `<` |
| `{item}` | 单字母代码 |

### 核心格式项分类

#### 文件信息类
| 代码 | 类型 | 含义 |
|------|------|------|
| `f` | S | 相对路径文件名 |
| `F` | S | 绝对路径文件名 |
| `t` | S | 文件名尾部 |
| `m` | F | 修改标记 `[+]`/`[-]` |
| `M` | F | 修改标记 `,+`/`,-` |
| `r` | F | 只读标记 `[RO]` |
| `R` | F | 只读标记 `,RO` |
| `h` | F | 帮助缓冲区 `[help]` |
| `H` | F | 帮助缓冲区 `,HLP` |
| `w` | F | 预览窗口 `[Preview]` |
| `W` | F | 预览窗口 `,PRV` |
| `y` | F | 文件类型 `[vim]` |
| `Y` | F | 文件类型 `,VIM` |

#### 光标位置类
| 代码 | 类型 | 含义 |
|------|------|------|
| `l` | N | 当前行号 |
| `L` | N | 总行数 |
| `c` | N | 列号（字节索引） |
| `v` | N | 虚拟列号（屏幕列） |
| `V` | N | 虚拟列号 `-N` 形式（等于c时不显示） |

#### 进度百分比类
| 代码 | 类型 | 含义 |
|------|------|------|
| `p` | N | 文件中光标行百分比（如 CTRL-G） |
| `P` | S | 视口百分比（Top/Bot/All/NN%） |

#### 高亮与分组
| 代码 | 含义 |
|------|------|
| `%#HLname#` | 设置高亮组 |
| `%*` / `%0*` | 恢复默认高亮 |
| `%N*` | 设置 User{N} 高亮（1-9） |
| `%=` | 分隔点，右侧内容右对齐，双 `%=` 形成三段式 |
| `%<` | 截断标记，宽度不足时从此处截断 |
| `%(` ... `%)` | 分组，支持整体 minwid/maxwid |

#### 表达式求值
| 代码 | 含义 |
|------|------|
| `%{expr}` | 求值表达式，结果直接插入 |
| `%{expr%}` | 求值表达式，结果再作为格式串二次展开 |
| `%0{expr}` | 结果按字面量插入，不做 flag 处理 |
| `%!expr()` | 整个 statusline 作为表达式求值 |

#### 点击区域
| 代码 | 含义 |
|------|------|
| `%[FuncName]` | 开始点击区域 |
| `%[]` | 结束点击区域 |
| `%N[FuncName]` | 带 minwid 标识的点击区域 |

#### 多行支持
| 代码 | 含义 |
|------|------|
| `%@` | 换行（需 `statuslineopt=maxheight:N` 且 N>1） |

## termd 优化实现

### 新增文件：`core/statusline.go`

实现完整的 Vim 兼容状态栏解析器：

```go
// 核心类型
type stlItemType int
const (
    stlTypeText      // 普通文本
    stlTypeItem      // 格式项
    stlTypeHighlight // 高亮控制
    stlTypeSeparate  // 分隔点 %=
    stlTypeTrunc     // 截断标记 %<
    stlTypeGroup     // 分组 (% ... %)
    stlTypeClickFunc // 点击区域 %[Func]%[]
    stlTypeLineBreak // 换行 %@
)

// 解析器
type stlParser struct {
    fmt        string
    items      []stlItem
    hltab      []stlHighlightRec
    clicktab   []stlClickRec
    separators []int
    carryHL    int
    width      int
    fillchar   rune
    wp         *EditorModel
    // ...
}

// 核心方法
func (m *EditorModel) buildStatusLine(fmtStr string, width int, multiLine bool) []string
func (p *stlParser) parseFormat()           // 解析格式串
func (p *stlParser) parseStatusItem()       // 解析单个格式项
func (p *stlParser) renderItems() string    // 单行渲染
func (p *stlParser) renderMultiLine() []string // 多行渲染
```

### 支持的格式项完整映射

| Vim 代码 | termd 实现 | 说明 |
|----------|------------|------|
| `%f` | `STL_FILEPATH` | 相对路径 |
| `%F` | `STL_FULLPATH` | 绝对路径 |
| `%t` | `STL_FILENAME` | 文件名尾部 |
| `%m` | `STL_MODIFIED` | `[+]`/`[-]` |
| `%M` | `STL_MODIFIED2` | `,+`/`,-` |
| `%r` | `STL_READONLY` | `[只读]` |
| `%R` | `STL_READONLY2` | `,只读` |
| `%h` | `STL_HELP` | `[帮助]` |
| `%H` | `STL_HELP2` | `,帮助` |
| `%w` | `STL_PREVIEW` | `[预览]` |
| `%W` | `STL_PREVIEW2` | `,预览` |
| `%y` | `STL_FILETYPE` | `[filetype]` |
| `%Y` | `STL_FILETYPE2` | `,filetype` |
| `%l` | `STL_LINENR` | 当前行号 |
| `%L` | `STL_LINES` | 总行数 |
| `%c` | `STL_COL` | 列号 |
| `%v` | `STL_VIRTCOL` | 虚拟列号 |
| `%V` | `STL_VIRTCOL2` | 虚拟列差值 |
| `%p` | `STL_PERCENT` | 文件进度百分比 |
| `%P` | `STL_VIEWPORTPCT` | 视口进度 |
| `%{expr}` | `STL_VIM_EXPR` | 表达式求值 |
| `%#HL#` | `STL_USER_HL` | 高亮组 |
| `%=` | `STL_SEPARATE` | 对齐分隔 |
| `%<` | `STL_TRUNCMARK` | 截断点 |
| `(%...%)` | `STL_GROUP_START/END` | 分组 |
| `%[Func]%[]` | `STL_CLICKFUNC` | 点击区域 |
| `%@` | `STL_LINEBREAK` | 换行 |

### 高亮系统

```go
var (
    highlightUser  []lipgloss.Style  // User1-9
    highlightStl   []lipgloss.Style  // StatusLine
    highlightStlnc []lipgloss.Style  // StatusLineNC
    highlightStlterm []lipgloss.Style  // Terminal StatusLine
    highlightStltermnc []lipgloss.Style // Terminal StatusLineNC
)

func initHighlights() {
    // User1-9: 亮色前景 + 深色背景
    highlightUser = []lipgloss.Style{
        lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Background(lipgloss.Color("0")),
        // ... 2-9
    }
    // StatusLine: 白前景 + 灰阶背景
    highlightStl = []lipgloss.Style{
        lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")),
        // ... 238, 240, 242, 244, 246, 248, 250, 252
    }
    // StatusLineNC: 暗前景 + 更深背景
    highlightStlnc = []lipgloss.Style{
        lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("234")),
        // ...
    }
}
```

高亮应用逻辑（仿 Vim `screen.c:1580-1605`）：

```go
curattr = baseAttr
for _, hl := range hltab {
    // 输出 hl.start 前的文本
    // 切换高亮：
    if hl.userhl == 0:
        curattr = baseAttr
    else if hl.userhl < 0:
        curattr = syn_id2attr(-hl.userhl)  // 语法高亮组
    else if isTerminal && !isCurrentWin:
        curattr = highlightStltermnc[hl.userhl-1]
    else if isTerminal:
        curattr = highlightStlterm[hl.userhl-1]
    else if !isCurrentWin:
        curattr = highlightStlnc[hl.userhl-1]
    else:
        curattr = highlightUser[hl.userhl-1]
}
```

### 默认状态栏格式

```go
const defaultStatusLineFormat = "%<%f %h%w%m%r%=%-14.(%l,%c%V%) %P"
```

对应 Vim 默认：`set statusline=%<%f\ %h%w%m%r%=%-14.(%l,%c%V%)\ %P`

### 多行状态栏支持

配合 `statuslineopt=maxheight:N,fixedheight`：

```go
// 启用多行
format := "%f %m %r%=%l:%c %P@%{getcwd()}@%{strftime('%H:%M')}"

lines := m.buildStatusLine(format, width, true)
// lines[0]: 文件名 修改 只读 += 行:列 进度
// lines[1]: 当前工作目录
// lines[2]: 当前时间
```

### 点击区域回调

```go
// 格式串中定义
format := "%[StatusClick]%f%[] %l:%c"

// 回调签名
func StatusClick(info map[string]interface{}) int {
    // info 包含：
    // minwid: int      // %N[Func] 的 N
    // nclicks: int     // 1/2/3
    // button: string   // "l"/"m"/"r"
    // mods: string     // "s"/"c"/"a" 组合
    // winid: int       // 窗口ID
    // area: string     // "statusline"/"tabline"/"tabpanel"
    // tabnr: int       // tabpanel 时的标签页号
    
    if info["button"] == "l" && info["nclicks"] == 2 {
        // 双击打开文件浏览器
        return 1  // 返回非零触发重绘
    }
    return 0
}
```

### 表达式求值沙箱

```go
// %{} 表达式在沙箱中求值（防止模式行注入）
// 可用变量：
//   g:actual_curbuf  真实当前缓冲区编号
//   g:actual_curwin  真实当前窗口ID
//   v:lnum           当前行号
//   v:col            当前列号
//   v:vircol         虚拟列号

// 示例：显示 Git 分支
format := "%{get(b:, 'git_branch', '')}"

// 示例：条件显示 LSP 诊断
format := "%{luaeval('vim.diagnostic.get(0, {lnum=line(\".\")-1})')}"
```

## 配置选项

### statuslineopt (stlo)

```go
type StatusLineOpt struct {
    FixedHeight bool   // 固定高度
    MaxHeight   int    // 最大高度（默认1，≥2启用多行）
}

// 解析 "maxheight:3,fixedheight"
func parseStatusLineOpt(s string) StatusLineOpt {
    // ...
}
```

### 运行时动态修改

```go
// 设置自定义状态栏
m.SetStatusLineFormat("%#User1# %m %f %#User2#%=%l:%c %P")

// 模式特定状态栏
m.SetStatusLineFormat(map[termd.Mode]string{
    termd.ModeEdit:      "%#User1# INSERT %f %m%r%=%l:%c %P",
    termd.ModeNormal:    "%#User2# NORMAL %f %m%r%=%l:%c %P",
    termd.ModePreview:   "%#User3# PREVIEW %f %=%l:%c %P",
    termd.ModeCommand:   "%#User4# COMMAND %f %=%l:%c %P",
})
```

## 迁移指南

### 从旧版 statusBar() 迁移

**旧代码**：
```go
func (m *EditorModel) statusBar() string {
    // 硬编码的左/右段拼接
    leftText := fmt.Sprintf(" %s  %s %s %s ", mode, fname, encTag, flags)
    rightText := fmt.Sprintf("%d,%d  %s  %dL%s ", rowLine+1, col, progress, total, lnMode)
    // ...
}
```

**新代码**：
```go
func (m *EditorModel) statusBar() string {
    format := "%<%f %h%w%m%r%=%-14.(%l,%c%V%) %P"
    lines := m.buildStatusLine(format, m.width, false)
    return m.applyHighlight(lines[0])
}

// 或使用预设
func (m *EditorModel) statusBar() string {
    return m.statusBarV2()  // 内置增强版
}
```

### 自定义格式示例

```go
// 1. 简约模式
"%f %m%r %= %l:%c %p%%"

// 2. 完整信息（仿 vim-airline）
"%#User1# %{mode()} %#User2# %f %m%r%h%w %#User3#%= %#User4#%{lsp_status()} %#User5# %l:%c %P"

// 3. 多行（需配置 statuslineopt=maxheight:3）
"%f %m%r%h%w%=%l:%c %P@%{getcwd()}@%{strftime('%Y-%m-%d %H:%M')}"

// 4. 可点击文件名
"%[BufClick]%f%[] %m%r %= %l:%c %P"
```

## 性能优化

### 缓存机制

```go
type cachedStatusLine struct {
    format     string
    width      int
    mode       termd.Mode
    cursorRow  int
    cursorCol  int
    bufModTime int64
    result     string
}

func (m *EditorModel) getCachedStatusLine(format string, width int) string {
    key := cacheKey{format, width, m.sm.Mode(), m.cursorRow, m.cursorCol, m.Buf.ModTime()}
    if cached, ok := m.stlCache[key]; ok {
        return cached
    }
    result := m.buildStatusLine(format, width, false)[0]
    m.stlCache[key] = result
    return result
}
```

### 增量更新

```go
// 仅在以下情况重新计算：
// - 格式串变化
// - 窗口宽度变化
// - 模式切换
// - 光标移动（行/列）
// - 缓冲区修改状态变化
// - 文件名/编码/文件类型变化
```

## 测试用例

```go
func TestStatusLineParser(t *testing.T) {
    tests := []struct {
        format   string
        width    int
        expect   string
    }{
        {"%f", 80, "test.go"},
        {"%m", 80, "[+]"},
        {"%r", 80, "[只读]"},
        {"%l:%c", 80, "10:5"},
        {"%p%%", 80, "50%"},
        {"%P", 80, "Top"},
        {"%=%l", 80, "              10"},
        {"%<%-20f%=", 80, "test.go               "},
        {"%#User1#test%*", 80, "test"}, // 高亮测试需渲染后检查
    }
    for _, tc := range tests {
        m := newTestModel(tc.format)
        result := m.buildStatusLine(tc.format, tc.width, false)[0]
        assert.Equal(t, tc.expect, stripANSI(result))
    }
}
```

## 总结

本次优化实现了：

1. **完整的 Vim 兼容格式解析器** - 支持所有标准格式项、高亮、分组、截断、对齐
2. **表达式求值支持** - `%{expr}`、 `%{expr%}`、 `%!expr()`、 `%0{expr}`
3. **多行状态栏** - `%@` 换行 + `statuslineopt=maxheight:N`
4. **点击交互** - `%[FuncName]%[]` 回调机制
5. **高亮继承** - 跨行 `%#HL#` 高亮状态保持
6. **性能优化** - 缓存 + 增量更新
7. **沙箱安全** - 表达式求值隔离

使用方式：

```go
// 简单用法
m.SetStatusLineFormat("%f %m%r %= %l:%c %P")

// 高级用法
m.SetStatusLineFormat(map[termd.Mode]string{
    termd.ModeEdit:    "%#User1# INSERT %f %m%r %= %l:%c %P",
    termd.ModeNormal:  "%#User2# NORMAL %f %m%r %= %l:%c %P",
})
```

这使 termd 的状态栏具备了 Vim 级别的可定制性和扩展性。