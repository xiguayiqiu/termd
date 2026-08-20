# termd

`termd` 是一个运行于终端的轻量级 Markdown 编辑器，基于 [bubbletea](https://github.com/charmbracelet/bubbletea) 框架构建，提供**编辑**与**实时富文本预览**双模式。

- 预览采用「块级 glamour 渲染 + 行内正则渲染」的混合策略，效果类似 mdcat（支持表格、代码高亮、数学公式），同时保留极低的输入延迟。
- 编辑态采用 vim 式按键模型（INSERT / NORMAL 双态），支持计数、撤销、行号、搜索等。
- 内置 `:ex` 文件浏览器（**两栏**：左=文件列表，右=文本预览），可新建 / 删除 / 重命名文件与目录。
- **当前不支持图片渲染**：`![alt](url)` 在 Preview 中以占位符（`🖼 alt (url)`）显示，图形渲染（Kitty 图形协议 / 半块字符）尚未接入渲染主路径。
- 多语言界面（简体中文 / 英文），依据 `LC_ALL` / `LC_MESSAGES` / `LANG` 自动切换。

---

## 特性

- **Edit/Preview 双模式**：`Ctrl+E` 切换；Preview 模式块级语法走 glamour（`GFM + Footnote + Emoji`），行内语法走轻量正则。
- **行内语法即时高亮**：粗体、斜体、粗斜体、删除线、行内代码、链接、图片、emoji 简码 `:smile:`、行内数学 `$...$`、转义字符。
- **链接点击跳转**：Preview 模式左键点击渲染后的超链接，`http(s)` 用系统浏览器打开，本地路径（含相对路径）用系统默认程序打开（`xdg-open` / `open`）。
- **块级语法**：代码块（chroma 语言高亮，支持约 250 种编程语言，见[代码块高亮](#代码块高亮)）、表格（对齐线）、数学块 `$$...$$`、脚注、定义列表、分隔线、嵌套引用。
- **行号**：`:set nu`（绝对）/ `:set rnu`（相对）/ `:set nonu`（关闭）。
- **命令模式**：`:` 进入，支持 `:w` 保存、`:q` 退出、`:d` 系列删除、`:set` 配置等。
- **文件浏览**：Edit 模式按 `Ctrl+O` 打开文件浏览器，可新建 / 删除文件与目录。
- **搜索**：Preview 模式按 `/` 进入搜索并高亮匹配。
- **图片占位**：`![alt](url)` 显示为占位符（`🖼 alt (url)`）；图形渲染（Kitty / 半块字符）已实现于 `image.go` 但**当前未接入渲染主路径，暂不支持**。
- **fcitx5 适配**：启用焦点报告，避免中文输入法激活时随机插入脏字符。

---

## 安装
```bash
# 用户级安装
mkdir -p /home/$USER/.local/bin/ && cp termd ~/.local/bin/

# 系统级安装
sudo cp termd /usr/local/bin/

```

## 使用

```bash
# 打开并编辑指定文件
./termd example.md

# 新建未命名缓冲区（启动后可 :w 文件名 命名保存）
./termd

# 查看帮助
./termd --help

# 查看版本
./termd --version

# 查看 ~/.termdrc 可用配置项
./termd -rc
```

---

## 模式与按键

`termd` 以 **Preview 模式**（默认）为枢纽，通过 `i`/`e`/`a` 进入 Edit 模式、`:` 进入命令模式、`/` 进入搜索；Edit 与命令模式都以 Preview 为回流点，避免状态爆炸。

### 常用键位

| 按键 | 作用 |
| --- | --- |
| `Ctrl+E` | 切换 Edit / Preview 模式 |
| `i` / `e` / `a` | 进入 Edit（插入）模式 |
| `Esc` | 返回 NORMAL 导航态 / 退出命令模式 |
| `:` | 进入命令模式 |
| `/` | 进入搜索（Preview 模式） |
| `Ctrl+O` | 打开文件浏览器（Edit 模式） |

### Preview 模式

- `i` / `e` / `a`：进入编辑模式（`i`/`e` 行首插入，`a` 行尾插入）
- `:`：进入命令模式
- `/`：进入搜索模式
- `j` / `↓` / `Enter`：下移高亮行
- `k` / `↑`：上移高亮行
- `PgUp` / `PgDown`：翻页

### Edit 模式（vim 式）

Edit 模式内部再分 **INSERT（插入）** 与 **NORMAL（普通导航）** 两态：

- INSERT：`Esc` 退回 NORMAL；其余键直接写入缓冲区（支持 Tab 缩进、退格、方向键、Home/End 等）。
- NORMAL（仿 vim NORMAL）：
  - `i` / `a` / `I` / `A` / `o` / `O`：回到插入态（光标处 / 后 / 行首非空白 / 行尾 / 下方新建 / 上方新建）
  - `h j k l`：光标移动（支持计数）
  - `w`/`b`/`e`：词级移动；`W`/`B`/`E` 忽略标点
  - `0` / `^` / `$`：行首 / 首个非空白 / 行尾
  - `gg` / `G`：文件首行 / 末行
  - `{` `}` `(` `)`：段落 / 句子移动
  - `Ctrl+D` / `Ctrl+U` / `Ctrl+F` / `Ctrl+B`：半页 / 整页滚动
  - `zz` / `zt` / `zb`：光标居中 / 置顶 / 置底
  - `x` / `X` / `dw` / `dd`：删字符 / 删前字符 / 删词 / 删整行（可计数）
  - `u`：撤销
  - `Esc`：返回 Preview 模式

### 命令模式

进入方式：在 Preview 模式按 `:` 进入命令模式，输入命令后回车执行；`Esc` 取消。

| 命令 | 说明 |
| --- | --- |
| `:q` | 退出（有改动需 `:q!`） |
| `:q!` | 强制退出，丢弃未保存改动 |
| `:w` | 保存当前文件（需启动时指定文件名，或先 `:w 文件名` 命名） |
| `:w 文件名` | 另存为 / 创建指定文件（`%` 展开为当前文件名） |
| `:wq` / `:wq!` | 保存并退出 |
| `:u` | 撤销 |
| `:e [文件]` / `:e!` | 重新加载文件 / 强制重载 |
| `:set nu` / `:set number` | 显示绝对行号 |
| `:set rnu` / `:set relativenumber` | 显示相对行号 |
| `:set nonu` | 关闭行号 |
| `:set cursorblink` / `:set nocursorblink` | 开启 / 关闭光标闪烁 |
| `:set smoothscroll` / `:set nosmoothscroll` | 开启 / 关闭 vim 式行滚动（老终端可关闭） |
| `:ex` | 打开文件浏览器 |
| `:d` / `:dd` | 删除当前行 |
| `:%d` | 清空当前文件全部内容 |
| `:dk 行号` / `:dj 行号` | 从当前行向上 / 向下删除到指定行 |
| `:数字` | 跳转到指定行（如 `:12`） |
| `:help` / `:keymap` | 查看命令帮助 / 键位一览 |

### 鼠标

- **滚轮上下翻阅**：滚轮在内容区滚动当前行/高亮行（预览模式滚动高亮行，编辑模式移动光标行）；配合 `Shift`/`Ctrl` 整页翻页（仿 vim `do_mousescroll`）。
- **大纲拖动调宽**：大纲侧边栏打开时，把鼠标移到大纲与正文的分隔列（大纲右边界），按住左键左右拖动即可自由调整大纲宽度（仿 vim `win_drag_vsep_line`）。
- 大纲与正文之间有一条竖直分隔线（ASCII `|`）划分区域，拖拽该线即可调宽。
- 在大纲列内滚动滚轮时，滚动的是大纲选项卡（并与正文联动跳转）。

### 浏览渲染（vim 式平滑滚动）

滚动视口时，termd 采用与 [glow](https://github.com/charmbracelet/glow) 相同的「**视觉行偏移**」模型：内容预先渲染并软换行成一维视觉行数组，视口只维护一个整数偏移（`yOffset`），滚轮/翻页按固定视觉行步长移动它（`mouse_vert_step = 3`）。

- **Preview/Command 模式**：内容区注册为**终端滚动区**（bubbletea 的 `SyncScrollArea`/`ScrollDown`/`ScrollUp`，与 glow v2 的 HighPerformance viewport 同款机制）。连续滚动时终端用 DECSTBM 物理移动已有内容行，只绘制新进入视口的行——**不再整屏重绘，彻底消除闪烁**，配合 `previewScroll` 视觉行偏移，浏览体验与 vim/glow 一致。
- **Edit 模式**：`scroll` 仍是 vim 的 topline（buffer 行），滚轮/翻页通过 `editScrollByVisual` 以视觉行为单位换算 topline，长行软换行成多行时也不会「跳一大片」。
- 滚动区在进入编辑/命令模式、开关大纲、窗口尺寸变化、内容被编辑等时机自动失效并交还 bubbletea 渲染（一次全量重绘，无残留），下次滚动自动重建。
- `j`/`k` 移动光标/高亮行（buffer 行粒度），视口按视觉行跟随；`Ctrl+F/B` 整页、`Ctrl+D/U` 半页（视觉行粒度）；`gg`/`G` 首尾跳转。
- 若仍希望按 buffer 行滚动，可 `:set nosmoothscroll`（老终端兼容）；写入 `~/.termdrc`（`smoothscroll` / `nosmoothscroll`）可永久生效。

### 文件浏览器（`:ex`）

采用 **两栏布局**：左栏为当前目录文件/文件夹列表，右栏为光标选中项的预览区。
- **文本文件**：按行渲染内容（超 256KB 仅预览前 256KB）。
- **非文本（二进制）文件**：显示属性面板——名称、类型/扩展名（图片/音频/视频/压缩包/PDF/字体等）、大小（带 B/KB/MB/GB 单位）、权限位、最后修改时间。
- **目录 / 上级目录**：显示对应提示。

- `j` / `k` / `↓` / `↑`：左栏焦点下移动光标 / 右栏焦点下滚动预览
- `Enter`：进入目录 / 选择文件
- `Tab`：在左栏列表与右栏预览之间切换焦点
- `PgUp` / `PgDn`：左栏翻页移动 / 右栏整页滚动
- `d` / `t`：新建文件夹 / 新建文件（输入名称后回车）
- `r`：删除选中项（弹确认框，`y` 确认 / 其它取消）
- `m`：重命名选中项（预填原名，输入新名后回车确认）
- `Esc`：退出浏览器

---

## 配置文件 `~/.termdrc`

类似 `.bashrc` / `.vimrc`，每次启动时自动加载，使设置永久生效（无需每次手动 `:set`）。每行一条，空行与 `#` 注释被忽略，`set ` 前缀可省略（直接写 `nu` 亦可）。

### 完整配置项

| 配置项 | 等价写法 | 作用 | 默认值 |
| --- | --- | --- | --- |
| `nu` | `number` | 显示绝对行号 | 关闭 |
| `rnu` | `relativenumber` | 显示相对行号（当前行显示绝对行号） | 关闭 |
| `nonu` | `nonumber` / `norelativenumber` | 关闭行号 | **默认** |
| `cursorblink` | — | 开启硬件光标闪烁 | **开启** |
| `nocursorblink` | — | 关闭光标闪烁（光标常亮） | 关闭 |
| `fileicons` | — | 文件浏览器渲染 Nerd Font 图标（仅 Nerd Font 终端有效，普通终端会乱码） | 关闭 |
| `nofileicons` | — | 关闭文件浏览器图标 | **默认** |
| `smoothscroll` | — | vim 式行滚动（按视觉行滚动，滚轮/翻页逐屏幕行移动） | **开启** |
| `nosmoothscroll` | — | 关闭平滑滚动（老终端兼容，改为按 buffer 行滚动） | 关闭 |

所有配置项与运行时 `:set` 命令一一对应，写入 `~/.termdrc` 后永久生效；编辑中也可用 `:set <项>` 即时切换（退出后不保留）。

### 示例

```sh
# 绝对行号
set nu
# 相对行号（与 set nu 二选一）
set rnu
# 关闭行号（默认）
set nonu
# 光标闪烁
set cursorblink
set nocursorblink
# 文件浏览器 Nerd Font 图标（仅 Nerd Font 终端有效，普通终端会乱码，默认关闭）
set fileicons
set nofileicons
# vim 式行滚动（老终端若滚动异常可关闭，默认开启）
set smoothscroll
set nosmoothscroll
```

运行 `termd -rc` 可查看完整配置项帮助。

---

## 国际化

界面默认英文，当 `LC_ALL` / `LC_MESSAGES` / `LANG` 含 `zh` / `cn` / `chinese` 时自动切换为简体中文。所有文案以「中文原文」为 key，未命中译文时回退中文原文，保证不崩、不乱码。

---

## 架构概览

项目为**多包结构**（`go build -o termd ./cmd/termd` 构建）：

| 包 | 说明 |
| --- | --- |
| 根目录（`package termd`，import 路径 `termd`） | 编辑器基础组件：`Buffer` / `Renderer` / `StateMachine` / `FileBrowser` / `SwapManager`、行内/块级渲染、键位、i18n、recovery、fcitx5、UTF-8 工具 |
| `core/`（`package core`，import 路径 `termd/core`） | 编辑器模型层 `editorModel`：拆分后的 `model_*.go` 及其紧耦合的滚动/状态栏/鼠标/大纲/rc 配置处理 |
| `cmd/termd/`（`package main`） | 程序入口：参数解析、i18n 初始化、构造模型、加载 `.termdrc`、启动 bubbletea |

`model.go` 已按功能拆分为多个 `model_*.go`（均位于 `core/`），拆分映射见 `modeln/README.md`。

| 文件 | 职责 |
| --- | --- |
| `cmd/termd/main.go` | 启动入口：解析命令行参数、初始化 i18n、加载 `.termdrc`、构造并运行 bubbletea 程序 |
| `core/model_types.go` | `EditorModel` 结构体、常量、构造、`Init` 及 rune/byte 列换算 |
| `core/model_update.go` | `Update` 消息分发 |
| `core/model_edit.go` | Edit 模式按键处理 + 光标移动/删除/滚动辅助 |
| `core/model_preview.go` | Preview 模式按键处理 + `renderPreview` + 预览光标注入 |
| `core/model_command.go` | Command 模式按键 + `executeCommand` + `expandPercent`/`loadFile` |
| `core/model_filebrowser.go` | 文件浏览器按键处理 |
| `core/model_view.go` | `View`/`fallbackView`/`frameLine` + `renderEdit` |
| `core/model_rebuild.go` | `rebuildPreview`（block-aware 渲染缓存构建） |
| `core/model_render_util.go` | 行号/代码块/chroma 高亮等渲染工具 |
| `core/model_util.go` | `touch`/`contentHeight`/`clamp`/`max`/`min` 等工具 |
| `statemachine.go` | 三模式状态机（`ModePreview` / `ModeEdit` / `ModeCommand`）及 Edit 子态、`LineNumMode` 行号模式 |
| `buffer.go` | 行存储缓冲区（`[][]byte`），光标定位、插入/删除、撤销（undo）等编辑操作 |
| `renderer.go` | 混合渲染策略：单行富文本渲染、光标对齐、软换行（`WrapText`）、CJK 列宽计算、glamour 单行渲染 |
| `markdown_render.go` | 行内语法轻量正则渲染（`RenderInline`）、块级 glamour 渲染（`RenderBlock`）、块级语法识别（代码块/表格/数学块/脚注/定义列表） |
| `keymap.go` | 键盘操作单一事实来源（`DefaultKeyMap`），生成键位/命令帮助视图（`:help` / `:keymap`） |
| `filebrowser.go` | `:ex` 文件浏览器：两栏布局（左列表 / 右文本预览）、新建/删除/重命名文件与目录、Nerd Font 图标着色 |
| `image.go` | 图片模块：远程（带缓存、异步）/ 本地（位图 + SVG 光栅化）、Kitty 图形协议 / 半块字符回退均已实现但**未接入渲染主路径**（当前图片显示为 `🖼` 占位符） |
| `i18n.go` | 中英文国际化（`T` / `Tf`），依据环境变量自动检测语言 |
| `fcitx5.go` | Linux fcitx5 输入法适配：焦点报告 + 输入过滤器，避免脏字符污染文本缓冲 |
| `recovery.go` | 崩溃恢复底层工具：swap 元数据编解码、原子写入（临时文件 + `os.Rename`）、PID/时间戳判定「正常退出 / 崩溃 / 被占用」 |
| `swap.go` | 后台并发写盘（`SwapManager`）：UI 线程采快照 → channel → 后台线程写 `.swp`，节流防卡 UI、优雅退出删除 `.swp`、崩溃残留供恢复 |
| `utf8util.go` | UTF-8 rune 编解码薄封装 |
| `core/scroll_render.go` | 视觉行滚动（借鉴 glow viewport 模型）：`previewScroll` 视觉行偏移、`editScrollByVisual` 视觉行换算，滚轮/翻页按视觉行步进，内容区增量/全量重绘与清除（`SyncScrollArea` / `ScrollDown` / `ScrollUp` / `ClearScrollArea`） |
| `core/statusbar.go` | 状态栏 / 命令栏（底部固定栏）：两段式状态栏（左模式块 / 右标尺块）、命令栏前缀高亮 + 实心光标块 |
| `core/mouse.go` | 鼠标支持：滚轮翻页（仿 vim `do_mousescroll`，无修饰键 3 行 / Shift/Ctrl 整页）、大纲分隔列拖动调宽（`win_drag_vsep_line`）、链接点击跳转（`openLink`：http(s) / 裸域名补协议浏览器打开、本地路径系统默认程序） |
| `core/outline.go` | Markdown 大纲侧边栏（仿 tagbar / `:Toc`）：`Ctrl+T` 切换、`#`~`######` 标题目录、`j`/`k` 移动高亮联动定位、Enter 跳转锁定 / Esc 关闭、与 `:ex` 互斥 |
| `core/termdrc.go` | 解析 `~/.termdrc` 持久配置并应用到编辑器 |

### 渲染策略（核心难点）

Preview 模式采用 **block-aware 混合渲染**：

- **行内语法**（粗体 / 斜体 / 删除线 / 行内代码 / 链接 / 图片 / emoji / 数学 `$...$` / 转义）走轻量正则 `RenderInline`，逐行、零延迟、不依赖 TTY。
- **块级语法**（代码块 ```` ``` ```` / 表格 / 数学块 `$$` / 脚注）整块送 **glamour**（`Renderer.RenderBlock`）渲染，保证表格对齐线、代码 chroma 高亮、分隔线正确。
- 普通段落行也通过 `inlineMDRE` 检测并渲染其中的行内语法。

这套方案渲染行为稳定可控，且保留 1:1 行号（block 内部多行共享首个 buffer 行号，行号列仅首行显示）。

### 代码块高亮

Preview 模式中的围栏代码块使用 **chroma**（v2.14.0）做语法高亮：

```go
func main() {
    fmt.Println("hello")
}
```

- **识别方式**：` ```语言名 ```` 围栏内的内容作为代码块整体送 glamour 渲染（chroma 负责高亮）。
- **支持语言**：约 **250 种**——240 个嵌入式 lexer（移植自 Pygments）+ 数十个手写 lexer，覆盖 Go、Python、Rust、TypeScript、JavaScript、Java、C、C++、C#、Ruby、PHP、Swift、Kotlin、SQL、Bash/Shell、YAML、JSON、HTML、CSS、Markdown、LaTeX、Diff、Dockerfile、Makefile 等主流语言。
- **匹配规则**（chroma `lexers.Get`）：按 **名称/别名**（如 `golang`→Go、`py`→Python、`cpp`/`c++`→C++、`js`→JavaScript）→ **大小写不敏感**（`GO`、`Python` 均可）→ **扩展名/文件名** 三级匹配。
- **降级行为**：无语言标注（```` ``` ````）或未匹配到任何语言的标签（如 ` ```text ````、` ```none ````、拼写错误）不会报错，代码块按**纯文本**原样展示（终端默认前景色，不染色），见 `core/model_render_util.go::highlightCode`。

---

## 依赖

核心依赖（见 `go.mod`）：

- `charm.land/glamour/v2` —— Markdown 渲染（GFM + Emoji）
- `github.com/charmbracelet/bubbletea` —— TUI 框架
- `github.com/charmbracelet/lipgloss` —— 样式
- `github.com/muesli/termenv` —— 终端色彩能力探测
- `github.com/yuin/goldmark` —— Markdown 解析（GFM + Footnote 扩展）

---

## License

见仓库 LICENSE 文件。
