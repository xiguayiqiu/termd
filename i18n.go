package main

// ============================================================
// i18n —— 国际化（中/英）支持
// ============================================================
//
// 设计：以「中文原文」作为字典 key。
//   - 中文环境：T(key) 直接返回 key 自身，零映射风险。
//   - 英文环境：在 enDict 中查找 key 对应的英文译文；若缺失则回退到 key（中文原文），保证不崩、不乱码。
//
// 用法：
//   - 纯文本：m.status = T("已保存")
//   - 带参数：m.status = Tf("已保存 %s", path)   // 占位符数量/顺序须与译文一致
//
// 语言自动检测：读取环境变量 LC_ALL / LC_MESSAGES / LANG，
//   值中含 "zh"/"cn"/"chinese" 判定为中文，否则默认英文。
//   也可通过 SetLang() 强制指定。

import (
	"fmt"
	"os"
	"strings"
)

// Lang 表示当前界面语言。
type Lang string

const (
	LangZH Lang = "zh" // 简体中文
	LangEN Lang = "en" // English
)

// currentLang 为当前生效语言，由 InitI18N 或 SetLang 设置。
var currentLang = LangEN

// zhDict 以中文原文为 key，值即中文原文（保持原样，便于回退与一致性）。
var zhDict = map[string]string{
	// --- 模式提示 ---
	"INSERT（Esc 进入 NORMAL 导航态）":    "INSERT（Esc 进入 NORMAL 导航态）",
	"NORMAL（导航态，i/a/I/A/o/O 回到插入）": "NORMAL（导航态，i/a/I/A/o/O 回到插入）",
	"INSERT（Esc 回到 NORMAL）":        "INSERT（Esc 回到 NORMAL）",
	"NORMAL（Esc 退出编辑）":             "NORMAL（Esc 退出编辑）",
	"已返回预览":                        "已返回预览",
	"已关闭键位帮助":                      "已关闭键位帮助",
	"键位帮助（Esc 关闭）":                 "键位帮助（Esc 关闭）",

	// --- 编辑操作反馈 ---
	"已插入缩进":  "已插入缩进",
	"已反缩进":   "已反缩进",
	"已撤销":    "已撤销",
	"无可撤销操作": "无可撤销操作",
	"已取消":    "已取消",
	"已取消命令":  "已取消命令",

	// --- 删除命令反馈 ---
	"已清空当前文件所有内容":                      "已清空当前文件所有内容",
	"用法: :dk 行号 / :dj 行号（行号为 1-based）": "用法: :dk 行号 / :dj 行号（行号为 1-based）",
	"已删除第 %d 行":                        "已删除第 %d 行",
	"已删除第 %d-%d 行":                     "已删除第 %d-%d 行",

	// --- 保存 / 加载 ---
	"有未保存改动，使用 :q! 强制退出": "有未保存改动，使用 :q! 强制退出",
	"保存失败: ": "保存失败: ",
	"已保存 ":   "已保存 ",
	"无文件名（缓冲区未命名，使用 :w 文件名 指定）": "无文件名（缓冲区未命名，使用 :w 文件名 指定）",
	"用法: :w 文件名（创建/另存为）":        "用法: :w 文件名（创建/另存为）",
	"已保存为 ": "已保存为 ",
	"无文件名（缓冲区未命名，使用 :e 文件名 指定）": "无文件名（缓冲区未命名，使用 :e 文件名 指定）",
	"加载失败: ": "加载失败: ",
	"已重新加载 ": "已重新加载 ",
	"未保存的修改，使用 :e! 强制重载": "未保存的修改，使用 :e! 强制重载",
	"文件浏览器已打开":           "文件浏览器已打开",

	// --- 行号设置 ---
	"行号显示: 绝对行号 (nu)":  "行号显示: 绝对行号 (nu)",
	"行号显示: 相对行号 (rnu)": "行号显示: 相对行号 (rnu)",
	"行号显示: 关闭 (nonu)":  "行号显示: 关闭 (nonu)",

	// --- 跳转 / 搜索 ---
	"跳转到第 %d 行":       "跳转到第 %d 行",
	"搜索关键字为空":         "搜索关键字为空",
	"匹配 '%s' 于第 %d 行": "匹配 '%s' 于第 %d 行",
	"未找到 '%s'":        "未找到 '%s'",

	// --- 当前文件 / 未知命令 ---
	"当前文件: （未命名缓冲区）": "当前文件: （未命名缓冲区）",
	"当前文件: ":         "当前文件: ",
	"未知命令: ":         "未知命令: ",

	// --- 启动错误 ---
	"初始化失败: %v\n": "init failed: %v\n",

	// --- 文件浏览器相关 ---
	"已退出文件浏览器":                       "已退出文件浏览器",
	"打开失败: ":                         "打开失败: ",
	"已打开 ":                           "已打开 ",
	"输入文件夹名后回车确认":                    "输入文件夹名后回车确认",
	"输入文件名后回车确认":                     "输入文件名后回车确认",
	"名称无效（不能为空/含 / /以 . 开头）":         "名称无效（不能为空/含 / /以 . 开头）",
	"已存在，未创建: ":                      "已存在，未创建: ",
	"创建失败: ":                         "创建失败: ",
	"已创建文件夹: ":                       "已创建文件夹: ",
	"已创建文件: ":                        "已创建文件: ",
	"不能删除上级目录":                       "不能删除上级目录",
	"已取消删除":                          "已取消删除",
	"删除失败: ":                         "删除失败: ",
	"已删除: ":                          "已删除: ",
	"重命名: ":                          "重命名: ",
	"输入新名称后回车确认":                     "输入新名称后回车确认",
	"已存在，未重命名: ":                     "已存在，未重命名: ",
	"重命名失败: ":                        "重命名失败: ",
	"已重命名: ":                         "已重命名: ",
	"文件 (j/k 移动, Enter 打开, Tab 切换) ": "文件 (j/k 移动, Enter 打开, Tab 切换) ",
	"预览 (Tab 切换焦点) ":                 "预览 (Tab 切换焦点) ",
	"（无可预览项）":                        "（无可预览项）",
	"（上级目录）":                         "（上级目录）",
	"（目录，无法预览）":                      "（目录，无法预览）",
	"（非文本文件，无法预览）":                   "（非文本文件，无法预览）",
	"二进制文件":                          "二进制文件",
	"图片":                             "图片",
	"音频":                             "音频",
	"视频":                             "视频",
	"压缩包":                            "压缩包",
	"PDF 文档":                         "PDF 文档",
	"字体":                             "字体",
	"类型":                             "类型",
	"大小":                             "大小",
	"权限":                             "权限",
	"修改时间":                           "修改时间",

	// --- 帮助视图标题 ---
	"termd 键位一览（Esc 关闭本视图）": "termd 键位一览（Esc 关闭本视图）",

	// --- 键位分组名 ---
	"预览模式":      "预览模式",
	"插入态":       "插入态",
	"普通导航态":     "普通导航态",
	"命令模式":      "命令模式",
	"命令模式命令":    "命令模式命令",
	"文件浏览器":     "文件浏览器",
	"文件浏览器(确认)": "文件浏览器(确认)",
	"文件浏览器(输入)": "文件浏览器(输入)",

	// --- 键位描述（keymap.go）---
	"进入编辑模式（i/e 行首插入，a 行尾插入）": "进入编辑模式（i/e 行首插入，a 行尾插入）",
	"进入命令模式（底部输入 : 命令）":       "进入命令模式（底部输入 : 命令）",
	"进入搜索模式": "进入搜索模式",
	"上移高亮行":  "上移高亮行",
	"下移高亮行":  "下移高亮行",
	"向上翻页":   "向上翻页",
	"向下翻页":   "向下翻页",
	"退回普通导航态（INSERT→NORMAL）":     "退回普通导航态（INSERT→NORMAL）",
	"输入字符直接写入缓冲区":                "输入字符直接写入缓冲区",
	"插入空格":                       "插入空格",
	"缩进（插入 TabSize 个空格）":         "缩进（插入 TabSize 个空格）",
	"反缩进（向左删除最多 TabSize 个空格）":    "反缩进（向左删除最多 TabSize 个空格）",
	"换行（在下方新建一行）":                "换行（在下方新建一行）",
	"删除（行首退格合并上一行）":              "删除（行首退格合并上一行）",
	"光标左移":                       "光标左移",
	"光标右移":                       "光标右移",
	"光标上移":                       "光标上移",
	"光标下移":                       "光标下移",
	"跳到行首":                       "跳到行首",
	"跳到行尾":                       "跳到行尾",
	"返回预览模式":                     "返回预览模式",
	"在光标处插入":                     "在光标处插入",
	"在光标后插入":                     "在光标后插入",
	"在行首非空白后插入":                  "在行首非空白后插入",
	"在行尾插入":                      "在行尾插入",
	"在下方新建一行并插入":                 "在下方新建一行并插入",
	"在上方新建一行并插入":                 "在上方新建一行并插入",
	"删除光标后字符（可计数）":               "删除光标后字符（可计数）",
	"删除光标前字符（可计数）":               "删除光标前字符（可计数）",
	"删除一个词（可计数）":                 "删除一个词（可计数）",
	"删除整行（可计数）":                  "删除整行（可计数）",
	"撤销":                         "撤销",
	"向后移动一个词（W 忽略标点）":            "向后移动一个词（W 忽略标点）",
	"向前移动一个词（B 忽略标点）":            "向前移动一个词（B 忽略标点）",
	"移动到词尾（E 忽略标点）":              "移动到词尾（E 忽略标点）",
	"跳到行首第一个非空白":                 "跳到行首第一个非空白",
	"g 组合等待（gj/gk 视觉行移动）":        "g 组合等待（gj/gk 视觉行移动）",
	"跳到文件首行（可计数）":                "跳到文件首行（可计数）",
	"跳到文件末行":                     "跳到文件末行",
	"上移一段（可计数）":                  "上移一段（可计数）",
	"下移一段（可计数）":                  "下移一段（可计数）",
	"上移一句（可计数）":                  "上移一句（可计数）",
	"下移一句（可计数）":                  "下移一句（可计数）",
	"向下翻半页（可计数）":                 "向下翻半页（可计数）",
	"向上翻半页（可计数）":                 "向上翻半页（可计数）",
	"向下翻一页":                      "向下翻一页",
	"向上翻一页":                      "向上翻一页",
	"光标居中":                       "光标居中",
	"光标置顶":                       "光标置顶",
	"光标置底":                       "光标置底",
	"取消命令，回到预览":                  "取消命令，回到预览",
	"执行命令":                       "执行命令",
	"删除命令行一个字符":                  "删除命令行一个字符",
	"输入空格":                       "输入空格",
	"输入字符追加到命令行":                 "输入字符追加到命令行",
	":q 退出（有改动需 :q!）":            ":q 退出（有改动需 :q!）",
	":q! 强制退出":                   ":q! 强制退出",
	":w 保存（:w 文件名 另存为）":          ":w 保存（:w 文件名 另存为）",
	":wq 保存并退出":                  ":wq 保存并退出",
	":u 撤销":                      ":u 撤销",
	":set nu / :set number 绝对行号": ":set nu / :set number 绝对行号",
	":set rnu 相对行号":              ":set rnu 相对行号",
	":set nonu 关闭行号":             ":set nonu 关闭行号",
	":ex 打开文件浏览器":                ":ex 打开文件浏览器",
	":e [文件] / :e! 重新加载文件":       ":e [文件] / :e! 重新加载文件",
	":d / :dd 删除当前行":             ":d / :dd 删除当前行",
	":%d 清空当前文件所有内容":             ":%d 清空当前文件所有内容",
	":dk 行号 从当前行向上删除到指定行":        ":dk 行号 从当前行向上删除到指定行",
	":dj 行号 从当前行向下删除到指定行":        ":dj 行号 从当前行向下删除到指定行",
	":help 查看命令模式命令帮助 / :keymap 查看键位表": ":help 查看命令模式命令帮助 / :keymap 查看键位表",
	"退出文件浏览器":     "退出文件浏览器",
	"下移光标":        "下移光标",
	"上移光标":        "上移光标",
	"进入目录 / 选择文件": "进入目录 / 选择文件",
	"创建文件夹":       "创建文件夹",
	"创建文件":        "创建文件",
	"删除选中项（弹确认）":  "删除选中项（弹确认）",
	"确认删除":        "确认删除",
	"取消删除":        "取消删除",
	"输入名称时退格":     "输入名称时退格",

	// --- 文件浏览器 Render 静态文案 ---
	"位置: ":     "位置: ",
	"确认删除 '":   "确认删除 '",
	"'? [y/n]": "'? [y/n]",
	"── :ex 文件浏览器 (j/k 移动, Enter 打开/选择, d 新建目录, t 新建文件, r 删除, Esc 退出) ──": "── :ex 文件浏览器 (j/k 移动, Enter 打开/选择, d 新建目录, t 新建文件, r 删除, Esc 退出) ──",
	"dirname: ":  "dirname: ",
	"filename: ": "filename: ",

	// --- 命令行 --help / --version ---
	"用法: %s [选项] [文件]\n": "用法: %s [选项] [文件]\n",
	"选项:":                "选项:",
	"显示帮助信息并退出":          "显示帮助信息并退出",
	"显示版本号并退出":           "显示版本号并退出",
	"文件:":                "文件:",
	"可选，启动后打开并编辑指定文件（缺省为新建未命名缓冲区）": "可选，启动后打开并编辑指定文件（缺省为新建未命名缓冲区）",
	"termd 版本 %s": "termd 版本 %s",

	// --- --help 详细说明 ---
	"termd 是一个运行于终端的轻量级 Markdown 编辑器，":   "termd 是一个运行于终端的轻量级 Markdown 编辑器，",
	"基于 bubbletea 框架构建，提供编辑与实时富文本预览双模式。": "基于 bubbletea 框架构建，提供编辑与实时富文本预览双模式。",
	"特性:": "特性:",
	"Edit/Preview 双模式：Ctrl+E 切换，预览采用块级 glamour 渲染（表格 / 代码高亮 / 数学公式）。": "Edit/Preview 双模式：Ctrl+E 切换，预览采用块级 glamour 渲染（表格 / 代码高亮 / 数学公式）。",
	"行内语法：粗体、斜体、删除线、行内代码、链接、图片、行内数学 $...$ 即时高亮。":                      "行内语法：粗体、斜体、删除线、行内代码、链接、图片、行内数学 $...$ 即时高亮。",
	"行号：:set nu（绝对）/ :set rnu（相对）/ :set nonu（关闭）。":                    "行号：:set nu（绝对）/ :set rnu（相对）/ :set nonu（关闭）。",
	"命令模式：: 进入，支持 :w 保存、:q 退出、:d 系列删除、:set 配置等。":                      "命令模式：: 进入，支持 :w 保存、:q 退出、:d 系列删除、:set 配置等。",
	"文件浏览：Edit 模式按 Ctrl+O 打开文件浏览器，可新建/删除文件与目录。":                       "文件浏览：Edit 模式按 Ctrl+O 打开文件浏览器，可新建/删除文件与目录。",
	"搜索：Preview 模式按 / 进入搜索并高亮匹配。":                                     "搜索：Preview 模式按 / 进入搜索并高亮匹配。",
	"多语言界面：依据 LC_ALL/LC_MESSAGES/LANG 自动切换简体中文 / 英文。":                 "多语言界面：依据 LC_ALL/LC_MESSAGES/LANG 自动切换简体中文 / 英文。",
	"常用键位:":                        "常用键位:",
	"Ctrl+E  切换 Edit / Preview 模式": "Ctrl+E  切换 Edit / Preview 模式",
	"i / a   进入 Edit（插入）模式":        "i / a   进入 Edit（插入）模式",
	"Esc     返回 NORMAL 导航态":        "Esc     返回 NORMAL 导航态",
	":       进入命令模式":               ":       进入命令模式",
	"/       进入搜索（Preview 模式）":     "/       进入搜索（Preview 模式）",
	"Ctrl+O  打开文件浏览器（Edit 模式）":     "Ctrl+O  打开文件浏览器（Edit 模式）",
	"命令模式示例:":                      "命令模式示例:",
	"保存文件":                         "保存文件",
	"退出 / 强制退出":                    "退出 / 强制退出",
	"显示绝对行号":                       "显示绝对行号",
	"删除当前行 / 清空全部内容":               "删除当前行 / 清空全部内容",
	"从当前行向上删除到第 N 行":               "从当前行向上删除到第 N 行",
	"从当前行向下删除到第 N 行":               "从当前行向下删除到第 N 行",
	"查看键位帮助":                       "查看键位帮助",

	// --- 命令模式命令帮助 (:help) ---
	"termd 命令模式命令帮助（Esc 关闭本视图）":            "termd 命令模式命令帮助（Esc 关闭本视图）",
	"进入方式：在预览模式按 : 进入命令模式，输入命令后回车执行。":      "进入方式：在预览模式按 : 进入命令模式，输入命令后回车执行。",
	"退出；若有未保存改动，提示改用 :q! 强制退出":             "Quit; if there are unsaved changes, prompt to use :q! to force quit",
	"强制退出，丢弃所有未保存改动":                       "Force quit, discarding all unsaved changes",
	"保存当前文件（需在启动时指定文件名，或先 :w 文件名 命名）":      "Save the current file (requires a filename from launch, or name it first via :w 文件名)",
	"另存为 / 创建指定文件（% 展开为当前文件名）":             "Save as / create the given file (% expands to the current filename)",
	"强制保存：文件不存在时自动创建，覆盖只读文件":               "Force save: auto-create if missing, overwrite read-only files",
	"保存并退出（:wq! 强制保存后退出）":                  "Save and quit (:wq! forces save then quits)",
	"保存到指定文件后退出":                           "Save to the given file then quit",
	"撤销上一次编辑操作":                            "Undo the last edit",
	"重新加载文件；无参等价于 :e %（重开当前文件）":            "Reload file; with no argument equals :e % (reopen current file)",
	"强制重新加载，丢弃未保存改动":                       "Force reload, discarding unsaved changes",
	":set nu 的等价写法（绝对行号）":                  ":set nu equivalent (absolute line numbers)",
	"显示相对行号（以当前行为基准）":                      "Show relative line numbers (relative to current line)",
	":set rnu 的等价写法":                       ":set rnu equivalent",
	"关闭行号显示":                               "Turn off line numbers",
	":set nonu 的等价写法（关闭行号）":                ":set nonu equivalent (line numbers off)",
	":set norelativenumber 的等价写法":          ":set norelativenumber equivalent",
	"打开文件浏览器（可新建 / 删除文件与目录）":               "Open the file browser (create / delete files and directories)",
	"删除当前高亮行（两者等价）":                        "Delete the current highlighted line (both equivalent)",
	"清空当前文件全部内容（保留一行空行）":                   "Clear all content of the current file (keeps one empty line)",
	"从当前行向上删除到指定行（含两端，行号 1-based）；无行号删到首行": "Delete upward from the current line to the given line (inclusive, 1-based); with no number, delete to the first line",
	"从当前行向下删除到指定行（含两端，行号 1-based）；无行号删到末行": "Delete downward from the current line to the given line (inclusive, 1-based); with no number, delete to the last line",
	"跳转到指定行（如 :12 跳到第 12 行，1-based）":       "Jump to the given line (e.g. :12 jumps to line 12, 1-based)",
	"显示当前文件名（未命名缓冲区显示「未命名」）":               "Show the current filename (shows \"unnamed\" for an unnamed buffer)",
	"查看本命令模式命令帮助（Esc 关闭）":                  "Show this command-mode help (Esc to close)",
	"查看键位帮助一览（Esc 关闭）":                     "Show key binding overview (Esc to close)",

	// --- 光标闪烁 ---
	":set cursorblink": ":set cursorblink",
	"开启光标闪烁（预览/命令模式的光标会闪烁）": "开启光标闪烁（预览/命令模式的光标会闪烁）",
	":set nocursorblink": ":set nocursorblink",
	"关闭光标闪烁（光标常亮不闪烁）":    "关闭光标闪烁（光标常亮不闪烁）",
}

// enDict 以「中文原文」为 key，值为英文译文。
// 仅当 key 命中且值为空字符串时回退到中文原文（保证不丢信息）。
var enDict = map[string]string{
	// --- 模式提示 ---
	"INSERT（Esc 进入 NORMAL 导航态）":    "INSERT (Esc to enter NORMAL nav mode)",
	"NORMAL（导航态，i/a/I/A/o/O 回到插入）": "NORMAL (nav mode, i/a/I/A/o/O to insert)",
	"INSERT（Esc 回到 NORMAL）":        "INSERT (Esc to NORMAL)",
	"NORMAL（Esc 退出编辑）":             "NORMAL (Esc to exit edit)",
	"已返回预览":                        "Back to preview",
	"已关闭键位帮助":                      "Keymap help closed",
	"键位帮助（Esc 关闭）":                 "Keymap help (Esc to close)",

	// --- 编辑操作反馈 ---
	"已插入缩进":  "Indent inserted",
	"已反缩进":   "Outdent applied",
	"已撤销":    "Undo",
	"无可撤销操作": "Nothing to undo",
	"已取消":    "Cancelled",
	"已取消命令":  "Command cancelled",

	// --- 删除命令反馈 ---
	"已清空当前文件所有内容":                      "Cleared all content",
	"用法: :dk 行号 / :dj 行号（行号为 1-based）": "Usage: :dk N / :dj N (N is 1-based line number)",
	"已删除第 %d 行":                        "Deleted line %d",
	"已删除第 %d-%d 行":                     "Deleted lines %d-%d",

	// --- 保存 / 加载 ---
	"有未保存改动，使用 :q! 强制退出": "Unsaved changes; use :q! to force quit",
	"保存失败: ": "Save failed: ",
	"已保存 ":   "Saved ",
	"无文件名（缓冲区未命名，使用 :w 文件名 指定）": "No filename (unnamed buffer; use :w filename)",
	"用法: :w 文件名（创建/另存为）":        "Usage: :w filename (create/save as)",
	"已保存为 ": "Saved as ",
	"无文件名（缓冲区未命名，使用 :e 文件名 指定）": "No filename (unnamed buffer; use :e filename)",
	"加载失败: ": "Load failed: ",
	"已重新加载 ": "Reloaded ",
	"未保存的修改，使用 :e! 强制重载": "Unsaved changes; use :e! to force reload",
	"文件浏览器已打开":           "File browser opened",

	// --- 行号设置 ---
	"行号显示: 绝对行号 (nu)":  "Line numbers: absolute (nu)",
	"行号显示: 相对行号 (rnu)": "Line numbers: relative (rnu)",
	"行号显示: 关闭 (nonu)":  "Line numbers: off (nonu)",

	// --- 跳转 / 搜索 ---
	"跳转到第 %d 行":       "Jumped to line %d",
	"搜索关键字为空":         "Empty search keyword",
	"匹配 '%s' 于第 %d 行": "Matched '%s' at line %d",
	"未找到 '%s'":        "Not found: '%s'",

	// --- 当前文件 / 未知命令 ---
	"当前文件: （未命名缓冲区）": "Current file: (unnamed buffer)",
	"当前文件: ":         "Current file: ",
	"未知命令: ":         "Unknown command: ",

	// --- 启动错误 ---
	"初始化失败: %v\n": "init failed: %v\n",

	// --- 文件浏览器相关 ---
	"已退出文件浏览器":                       "File browser closed",
	"打开失败: ":                         "Open failed: ",
	"已打开 ":                           "Opened ",
	"输入文件夹名后回车确认":                    "Type folder name, then Enter to confirm",
	"输入文件名后回车确认":                     "Type file name, then Enter to confirm",
	"名称无效（不能为空/含 / /以 . 开头）":         "Invalid name (empty / contains / / starts with .)",
	"已存在，未创建: ":                      "Already exists, not created: ",
	"创建失败: ":                         "Create failed: ",
	"已创建文件夹: ":                       "Created folder: ",
	"已创建文件: ":                        "Created file: ",
	"不能删除上级目录":                       "Cannot delete parent directory",
	"已取消删除":                          "Delete cancelled",
	"删除失败: ":                         "Delete failed: ",
	"已删除: ":                          "Deleted: ",
	"重命名: ":                          "rename: ",
	"输入新名称后回车确认":                     "Type new name, then Enter to confirm",
	"已存在，未重命名: ":                     "Already exists, not renamed: ",
	"重命名失败: ":                        "Rename failed: ",
	"已重命名: ":                         "Renamed: ",
	"文件 (j/k 移动, Enter 打开, Tab 切换) ": "Files (j/k move, Enter open, Tab switch) ",
	"预览 (Tab 切换焦点) ":                 "Preview (Tab switch focus) ",
	"（无可预览项）":                        "(nothing to preview)",
	"（上级目录）":                         "(parent directory)",
	"（目录，无法预览）":                      "(directory, no preview)",
	"（非文本文件，无法预览）":                   "(non-text file, no preview)",
	"二进制文件":                          "Binary file",
	"图片":                             "Image",
	"音频":                             "Audio",
	"视频":                             "Video",
	"压缩包":                            "Archive",
	"PDF 文档":                         "PDF document",
	"字体":                             "Font",
	"类型":                             "Type",
	"大小":                             "Size",
	"权限":                             "Perm",
	"修改时间":                           "Modified",

	// --- 帮助视图标题 ---
	"termd 键位一览（Esc 关闭本视图）": "termd keymap (Esc to close)",

	// --- 键位分组名 ---
	"预览模式":      "Preview Mode",
	"插入态":       "Insert Mode",
	"普通导航态":     "Normal Mode",
	"命令模式":      "Command Mode",
	"命令模式命令":    "Command-Mode Commands",
	"文件浏览器":     "File Browser",
	"文件浏览器(确认)": "File Browser (confirm)",
	"文件浏览器(输入)": "File Browser (input)",

	// --- 键位描述 ---
	"进入编辑模式（i/e 行首插入，a 行尾插入）": "Enter edit mode (i/e insert at BOL, a at EOL)",
	"进入命令模式（底部输入 : 命令）":       "Enter command mode (: at bottom)",
	"进入搜索模式": "Enter search mode",
	"上移高亮行":  "Move highlight up",
	"下移高亮行":  "Move highlight down",
	"向上翻页":   "Page up",
	"向下翻页":   "Page down",
	"退回普通导航态（INSERT→NORMAL）":     "Back to NORMAL (INSERT→NORMAL)",
	"输入字符直接写入缓冲区":                "Type character to insert",
	"插入空格":                       "Insert space",
	"缩进（插入 TabSize 个空格）":         "Indent (insert TabSize spaces)",
	"反缩进（向左删除最多 TabSize 个空格）":    "Outdent (delete up to TabSize spaces)",
	"换行（在下方新建一行）":                "Newline (new line below)",
	"删除（行首退格合并上一行）":              "Delete (backspace merges with above)",
	"光标左移":                       "Cursor left",
	"光标右移":                       "Cursor right",
	"光标上移":                       "Cursor up",
	"光标下移":                       "Cursor down",
	"跳到行首":                       "Go to line start",
	"跳到行尾":                       "Go to line end",
	"返回预览模式":                     "Return to preview mode",
	"在光标处插入":                     "Insert at cursor",
	"在光标后插入":                     "Append after cursor",
	"在行首非空白后插入":                  "Insert after first non-blank",
	"在行尾插入":                      "Insert at end of line",
	"在下方新建一行并插入":                 "Open line below and insert",
	"在上方新建一行并插入":                 "Open line above and insert",
	"删除光标后字符（可计数）":               "Delete char after cursor (count)",
	"删除光标前字符（可计数）":               "Delete char before cursor (count)",
	"删除一个词（可计数）":                 "Delete word (count)",
	"删除整行（可计数）":                  "Delete line (count)",
	"撤销":                         "Undo",
	"向后移动一个词（W 忽略标点）":            "Word forward (W ignores punctuation)",
	"向前移动一个词（B 忽略标点）":            "Word backward (B ignores punctuation)",
	"移动到词尾（E 忽略标点）":              "To word end (E ignores punctuation)",
	"跳到行首第一个非空白":                 "To first non-blank",
	"g 组合等待（gj/gk 视觉行移动）":        "g-prefix wait (gj/gk visual move)",
	"跳到文件首行（可计数）":                "Go to first line (count)",
	"跳到文件末行":                     "Go to last line",
	"上移一段（可计数）":                  "Paragraph up (count)",
	"下移一段（可计数）":                  "Paragraph down (count)",
	"上移一句（可计数）":                  "Sentence up (count)",
	"下移一句（可计数）":                  "Sentence down (count)",
	"向下翻半页（可计数）":                 "Half page down (count)",
	"向上翻半页（可计数）":                 "Half page up (count)",
	"向下翻一页":                      "Page down",
	"向上翻一页":                      "Page up",
	"光标居中":                       "Center cursor",
	"光标置顶":                       "Cursor to top",
	"光标置底":                       "Cursor to bottom",
	"取消命令，回到预览":                  "Cancel command, back to preview",
	"执行命令":                       "Execute command",
	"删除命令行一个字符":                  "Delete one char in command line",
	"输入空格":                       "Input space",
	"输入字符追加到命令行":                 "Append typed char to command line",
	":q 退出（有改动需 :q!）":            ":q quit (use :q! if changed)",
	":q! 强制退出":                   ":q! force quit",
	":w 保存（:w 文件名 另存为）":          ":w save (:w filename to save as)",
	":wq 保存并退出":                  ":wq save and quit",
	":u 撤销":                      ":u undo",
	":set nu / :set number 绝对行号": ":set nu / :set number absolute",
	":set rnu 相对行号":              ":set rnu relative",
	":set nonu 关闭行号":             ":set nonu off",
	":ex 打开文件浏览器":                ":ex open file browser",
	":e [文件] / :e! 重新加载文件":       ":e [file] / :e! reload file",
	":d / :dd 删除当前行":             ":d / :dd delete current line",
	":%d 清空当前文件所有内容":             ":%d clear all content",
	":dk 行号 从当前行向上删除到指定行":        ":dk N delete up to line N",
	":dj 行号 从当前行向下删除到指定行":        ":dj N delete down to line N",
	":help 查看命令模式命令帮助 / :keymap 查看键位表": ":help shows command help / :keymap shows key bindings",
	"退出文件浏览器":     "Exit file browser",
	"下移光标":        "Move cursor down",
	"上移光标":        "Move cursor up",
	"进入目录 / 选择文件": "Enter dir / select file",
	"创建文件夹":       "Create folder",
	"创建文件":        "Create file",
	"删除选中项（弹确认）":  "Delete selected (confirm)",
	"确认删除":        "Confirm delete",
	"取消删除":        "Cancel delete",
	"输入名称时退格":     "Backspace when typing name",

	// --- 文件浏览器 Render 静态文案 ---
	"位置: ":     "Location: ",
	"确认删除 '":   "Confirm delete '",
	"'? [y/n]": "'? [y/n]",
	"── :ex 文件浏览器 (j/k 移动, Enter 打开/选择, d 新建目录, t 新建文件, r 删除, m 重命名, Esc 退出) ──": "── :ex file browser (j/k move, Enter open/select, d new dir, t new file, r delete, m rename, Esc quit) ──",
	"dirname: ":  "dirname: ",
	"filename: ": "filename: ",

	// --- 命令行 --help / --version ---
	"用法: %s [选项] [文件]\n": "Usage: %s [options] [file]\n",
	"选项:":                "Options:",
	"显示帮助信息并退出":          "Show help and exit",
	"显示版本号并退出":           "Show version and exit",
	"文件:":                "File:",
	"可选，启动后打开并编辑指定文件（缺省为新建未命名缓冲区）": "Optional. Open and edit the given file on launch (defaults to a new unnamed buffer)",
	"termd 版本 %s": "termd version %s",

	// --- --help 详细说明 ---
	"termd 是一个运行于终端的轻量级 Markdown 编辑器，":   "termd is a lightweight terminal-based Markdown editor, ",
	"基于 bubbletea 框架构建，提供编辑与实时富文本预览双模式。": "built on the bubbletea framework, offering both editing and live rich-text preview modes.",
	"特性:": "Features:",
	"Edit/Preview 双模式：Ctrl+E 切换，预览采用块级 glamour 渲染（表格 / 代码高亮 / 数学公式）。": "Edit/Preview dual mode: toggle with Ctrl+E; preview uses block-level glamour rendering (tables / code highlighting / math).",
	"行内语法：粗体、斜体、删除线、行内代码、链接、图片、行内数学 $...$ 即时高亮。":                      "Inline syntax: bold, italic, strikethrough, inline code, links, images, and inline math $...$ are highlighted instantly.",
	"行号：:set nu（绝对）/ :set rnu（相对）/ :set nonu（关闭）。":                    "Line numbers: :set nu (absolute) / :set rnu (relative) / :set nonu (off).",
	"命令模式：: 进入，支持 :w 保存、:q 退出、:d 系列删除、:set 配置等。":                      "Command mode: enter with :; supports :w to save, :q to quit, the :d deletion family, :set config, and more.",
	"文件浏览：Edit 模式按 Ctrl+O 打开文件浏览器，可新建/删除文件与目录。":                       "File browser: in Edit mode press Ctrl+O to open the browser; create/delete files and directories.",
	"搜索：Preview 模式按 / 进入搜索并高亮匹配。":                                     "Search: in Preview mode press / to search and highlight matches.",
	"多语言界面：依据 LC_ALL/LC_MESSAGES/LANG 自动切换简体中文 / 英文。":                 "Multilingual UI: automatically switches between Simplified Chinese and English via LC_ALL/LC_MESSAGES/LANG.",
	"常用键位:":                        "Common key bindings:",
	"Ctrl+E  切换 Edit / Preview 模式": "Ctrl+E  Toggle Edit / Preview mode",
	"i / a   进入 Edit（插入）模式":        "i / a   Enter Edit (insert) mode",
	"Esc     返回 NORMAL 导航态":        "Esc     Return to NORMAL navigation state",
	":       进入命令模式":               ":       Enter command mode",
	"/       进入搜索（Preview 模式）":     "/       Enter search (Preview mode)",
	"Ctrl+O  打开文件浏览器（Edit 模式）":     "Ctrl+O  Open file browser (Edit mode)",
	"命令模式示例:":                      "Command mode examples:",
	"保存文件":                         "Save file",
	"退出 / 强制退出":                    "Quit / force quit",
	"显示绝对行号":                       "Show absolute line numbers",
	"删除当前行 / 清空全部内容":               "Delete current line / clear all content",
	"从当前行向上删除到第 N 行":               "Delete upward from current line to line N",
	"从当前行向下删除到第 N 行":               "Delete downward from current line to line N",
	"查看键位帮助":                       "Show key binding help",

	// --- 命令模式命令帮助 (:help) ---
	"termd 命令模式命令帮助（Esc 关闭本视图）":            "termd Command-Mode Help (Esc to close)",
	"进入方式：在预览模式按 : 进入命令模式，输入命令后回车执行。":      "Enter command mode from Preview by pressing :; type a command and press Enter to run it.",
	"退出；若有未保存改动，提示改用 :q! 强制退出":             "Quit; if there are unsaved changes, prompt to use :q! to force quit",
	"强制退出，丢弃所有未保存改动":                       "Force quit, discarding all unsaved changes",
	"保存当前文件（需在启动时指定文件名，或先 :w 文件名 命名）":      "Save the current file (requires a filename from launch, or name it first via :w 文件名)",
	"另存为 / 创建指定文件（% 展开为当前文件名）":             "Save as / create the given file (% expands to the current filename)",
	"强制保存：文件不存在时自动创建，覆盖只读文件":               "Force save: auto-create if missing, overwrite read-only files",
	"保存并退出（:wq! 强制保存后退出）":                  "Save and quit (:wq! forces save then quits)",
	"保存到指定文件后退出":                           "Save to the given file then quit",
	"撤销上一次编辑操作":                            "Undo the last edit",
	"重新加载文件；无参等价于 :e %（重开当前文件）":            "Reload file; with no argument equals :e % (reopen current file)",
	"强制重新加载，丢弃未保存改动":                       "Force reload, discarding unsaved changes",
	":set nu 的等价写法（绝对行号）":                  ":set nu equivalent (absolute line numbers)",
	"显示相对行号（以当前行为基准）":                      "Show relative line numbers (relative to current line)",
	":set rnu 的等价写法":                       ":set rnu equivalent",
	"关闭行号显示":                               "Turn off line numbers",
	":set nonu 的等价写法（关闭行号）":                ":set nonu equivalent (line numbers off)",
	":set norelativenumber 的等价写法":          ":set norelativenumber equivalent",
	"打开文件浏览器（可新建 / 删除文件与目录）":               "Open the file browser (create / delete files and directories)",
	"删除当前高亮行（两者等价）":                        "Delete the current highlighted line (both equivalent)",
	"清空当前文件全部内容（保留一行空行）":                   "Clear all content of the current file (keeps one empty line)",
	"从当前行向上删除到指定行（含两端，行号 1-based）；无行号删到首行": "Delete upward from the current line to the given line (inclusive, 1-based); with no number, delete to the first line",
	"从当前行向下删除到指定行（含两端，行号 1-based）；无行号删到末行": "Delete downward from the current line to the given line (inclusive, 1-based); with no number, delete to the last line",
	"跳转到指定行（如 :12 跳到第 12 行，1-based）":       "Jump to the given line (e.g. :12 jumps to line 12, 1-based)",
	"显示当前文件名（未命名缓冲区显示「未命名」）":               "Show the current filename (shows \"unnamed\" for an unnamed buffer)",
	"查看本命令模式命令帮助（Esc 关闭）":                  "Show this command-mode help (Esc to close)",
	"查看键位帮助一览（Esc 关闭）":                     "Show key binding overview (Esc to close)",

	// --- 光标闪烁 ---
	":set cursorblink": ":set cursorblink",
	"开启光标闪烁（预览/命令模式的光标会闪烁）": "Enable cursor blinking (cursor blinks in preview/command mode)",
	":set nocursorblink": ":set nocursorblink",
	"关闭光标闪烁（光标常亮不闪烁）":    "Disable cursor blinking (cursor stays solid)",

	// --- 鼠标 ---
	"拖动可调整大纲宽度": "Drag to resize the outline width",
	// --- 平滑行滚动 ---
	"平滑行滚动: 开启（vim 式行刷新）":        "Smooth line scrolling: on (vim-style row refresh)",
	"平滑行滚动: 关闭（老终端兼容，改为全量重绘）":    "Smooth line scrolling: off (full repaint, for older terminals)",
	"开启 vim 式行滚动（滚动区增量刷新，浏览更平滑）": "Enable smooth line scrolling (scroll-region incremental refresh)",
	"关闭平滑行滚动（老终端兼容，改为全量重绘）":      "Disable smooth line scrolling (full repaint, for older terminals)",
}

// InitI18N 依据环境变量检测当前语言。应在程序启动时调用一次。
func InitI18N() {
	currentLang = detectLang()
}

// SetLang 强制设置语言（测试或显式指定时可用）。
func SetLang(l Lang) {
	currentLang = l
}

// CurrentLang 返回当前生效语言。
func CurrentLang() Lang {
	return currentLang
}

// detectLang 读取 LC_ALL / LC_MESSAGES / LANG，含 zh/cn/chinese 判定为中文，否则英文。
func detectLang() Lang {
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.ToLower(os.Getenv(env))
		if v == "" {
			continue
		}
		// 取首段（如 zh_CN.UTF-8 -> zh_CN）
		base := v
		if i := strings.IndexAny(v, "._@"); i >= 0 {
			base = v[:i]
		}
		if strings.Contains(base, "zh") || base == "cn" || strings.Contains(base, "chinese") {
			return LangZH
		}
		return LangEN
	}
	return LangEN
}

// T 翻译给定 key（以中文原文为 key）。命中且译文非空则返回译文，否则回退 key 本身（中文原文）。
func T(key string) string {
	if currentLang == LangZH {
		return key
	}
	if v, ok := enDict[key]; ok && v != "" {
		return v
	}
	return key // 回退到中文原文
}

// Tf 带格式参数的翻译，等价于 fmt.Sprintf(T(key), args...)。
func Tf(key string, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}
