package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"termd"
	"termd/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

// ============================================================
// main —— 程序入口与 bubbletea 集成
// ============================================================
//
// 启动流程：
//  1. 通过 termenv 检测终端颜色能力（TrueColor / ANSI256 / ANSI），
//     并据此配置 lipgloss 的全局色彩配置，使富文本在各终端正确显示。
//  2. 解析命令行参数作为待编辑文件路径（可选）。
//  3. 构造 editorModel，运行 bubbletea 程序（启用鼠标/AltScreen 以获得全屏体验）。
//
// usageText 返回 --help 的帮助文本（含 i18n 文案）。
func usageText() string {
	return termd.Tf("用法: %s [选项] [文件]\n", "termd") +
		"\n" +
		termd.T("termd 是一个运行于终端的轻量级 Markdown 编辑器，") +
		termd.T("基于 bubbletea 框架构建，提供编辑与实时富文本预览双模式。") + "\n" +
		"\n" +
		termd.T("特性:") + "\n" +
		"  • " + termd.T("Edit/Preview 双模式：预览采用块级 glamour 渲染（表格 / 代码高亮）。") + "\n" +
		"  • " + termd.T("行内语法：粗体、斜体、删除线、行内代码、链接、图片、emoji 即时高亮。") + "\n" +
		"  • " + termd.T("行号：:set nu（绝对）/ :set rnu（相对）/ :set nonu（关闭）。") + "\n" +
		"  • " + termd.T("命令模式：: 进入，支持 :w 保存、:q 退出、:d 系列删除、:set 配置等。") + "\n" +
		"  • " + termd.T("文件浏览：Edit 模式按 Ctrl+O 打开文件浏览器，可新建/删除文件与目录。") + "\n" +
		"  • " + termd.T("搜索：Preview 模式按 / 进入搜索并高亮匹配。") + "\n" +
		"  • " + termd.T("多语言界面：依据 LC_ALL/LC_MESSAGES/LANG 自动切换简体中文 / 英文。") + "\n" +
		"\n" +
		termd.T("常用键位:") + "\n" +
		"  " + termd.T("i / a   进入 Edit（插入）模式") + "\n" +
		"  " + termd.T("Esc     返回 NORMAL 导航态") + "\n" +
		"  " + termd.T(":       进入命令模式") + "\n" +
		"  " + termd.T("/       进入搜索（Preview 模式）") + "\n" +
		"  " + termd.T("Ctrl+O  打开文件浏览器（Edit 模式）") + "\n" +
		"\n" +
		termd.T("命令模式示例:") + "\n" +
		"  :w         " + termd.T("保存文件") + "\n" +
		"  :q / :q!   " + termd.T("退出 / 强制退出") + "\n" +
		"  :set nu    " + termd.T("显示绝对行号") + "\n" +
		"  :d / :%d   " + termd.T("删除当前行 / 清空全部内容") + "\n" +
		"  :dk N      " + termd.T("从当前行向上删除到第 N 行") + "\n" +
		"  :dj N      " + termd.T("从当前行向下删除到第 N 行") + "\n" +
		"  :help      " + termd.T("查看键位帮助") + "\n" +
		"\n" +
		termd.T("选项:") + "\n" +
		"  -h, --help      " + termd.T("显示帮助信息并退出") + "\n" +
		"  -v, --version   " + termd.T("显示版本号并退出") + "\n" +
		"  -rc             " + termd.T("显示 ~/.termdrc 可用设置项并退出") + "\n" +
		"  -ml             " + termd.T("显示 termd 支持的 Markdown 语法教程并退出") + "\n" +
		"\n" +
		termd.T("文件:") + "\n" +
		"  " + termd.T("可选，启动后打开并编辑指定文件（缺省为新建未命名缓冲区）") + "\n"
}

// rcHelpText 返回 -rc 显示的 ~/.termdrc 可用设置项帮助文本。
// 这些设置项与运行时 :set 命令一一对应，写入 ~/.termdrc 后可永久生效。
func rcHelpText() string {
	home, _ := os.UserHomeDir()
	rcPath := "~/." + core.TermdrcName
	if home != "" {
		rcPath = filepath.Join(home, core.TermdrcName)
	}
	return termd.Tf("配置文件: %s\n", rcPath) +
		termd.T("(类似 .bashrc / .vimrc，启动自动加载。每行一条，# 开头为注释，可省略 set 前缀)\n") +
		"\n" +
		termd.T("可用设置项:") + "\n" +
		"  nu / number                 " + termd.T("显示绝对行号") + termd.T("（默认关闭）") + "\n" +
		"  rnu / relativenumber        " + termd.T("显示相对行号（光标行显示绝对行号）") + termd.T("（默认关闭）") + "\n" +
		"  nonu / nonumber / norelativenumber  " + termd.T("关闭行号") + termd.T("（默认）") + "\n" +
		"  cursorblink                 " + termd.T("开启硬件光标闪烁") + termd.T("（默认开启）") + "\n" +
		"  nocursorblink               " + termd.T("关闭硬件光标闪烁") + termd.T("（默认关闭）") + "\n" +
		"  fileicons                   " + termd.T("文件浏览器渲染 Nerd Font 图标（需 Nerd Font 终端）") + termd.T("（默认关闭）") + "\n" +
		"  nofileicons                 " + termd.T("关闭文件浏览器图标") + termd.T("（默认）") + "\n" +
		"  smoothscroll                " + termd.T("开启 vim 式平滑滚动（按视觉行滚动，滚轮/翻页逐屏幕行移动）") + termd.T("（默认开启）") + "\n" +
		"  nosmoothscroll              " + termd.T("关闭平滑滚动（老终端兼容，改为按 buffer 行滚动）") + termd.T("（默认关闭）") + "\n" +
		"\n" +
		termd.T("示例:") + "\n" +
		"  set nu\n" +
		"  cursorblink\n" +
		"  fileicons\n" +
		"\n" +
		termd.T("运行 `termd -rc` 查看本帮助；编辑中也可 :set <项> 即时切换。") + "\n"
}

func main() {
	// 初始化国际化：依据 LC_ALL/LC_MESSAGES/LANG 选择语言（默认英文，含 zh 判定中文）
	termd.InitI18N()

	// 解析全局命令行标志（--help/-h、--version/-v），其余参数视为待编辑文件路径
	args := os.Args[1:]
	var pathArgs []string
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Print(usageText())
			return
		case "-v", "--version":
			fmt.Println(termd.Tf("termd 版本 %s", termd.Version))
			return
		case "-rc":
			fmt.Print(rcHelpText())
			return
		case "-ml":
			// 打印 Markdown 语法教程后退出（内容见 termd.MarkdownLanguage.go）
			fmt.Print(termd.RenderMarkdownLanguage())
			return
		default:
			pathArgs = append(pathArgs, a)
		}
	}

	// 检测并设置终端颜色能力（termenv 自动判断 TrueColor / 256 / 16 色）
	profile := termenv.ColorProfile()
	_ = termenv.NewOutput(os.Stdout)

	// 待编辑文件路径：取第一个剩余命令行参数，缺省为空（新建文件）
	path := ""
	if len(pathArgs) > 0 {
		path = pathArgs[0]
	}

	model, err := core.NewModel(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, termd.T("初始化失败: %v\n"), err)
		os.Exit(1)
	}
	// 加载 ~/.termdrc 持久配置（类似 .vimrc），使其永久生效
	core.LoadTermdrc().ApplyTo(model)
	// 同步 termenv 能力到 renderer
	model.Rend.SetProfile(profile)

	// 启动 bubbletea：使用 AltScreen 获得全屏终端体验，并捕获 Ctrl+C 作为退出。
	// termd.WithFcitx5() 注入 fcitx5 输入法适配：启用焦点报告以正确解析 \x1b[I / \x1b[O，
	// 避免输入法激活时随机插入 i/o 等乱序字符。
	p := tea.NewProgram(model,
		append(
			[]tea.ProgramOption{
				tea.WithAltScreen(),       // 全屏
				tea.WithMouseCellMotion(), // 鼠标移动（预留）
			},
			termd.WithFcitx5()...,
		)...,
	)
	// 注入异步消息通道：后台图片加载完成时用于触发重绘（非阻塞，避免卡死 UI）
	model.SendMsg = func(msg tea.Msg) { p.Send(msg) }

	// 崩溃恢复：为已命名的文件启动后台 swap 写盘（未命名缓冲区不启用，无 .swp）。
	// 架构见 swap.go / recovery.go：快照在 UI 线程采集，写盘在后台线程完成，
	// 绝不阻塞输入与渲染。
	if model.Buf.FilePath() != "" {
		fp := model.Buf.FilePath()
		sw := termd.NewSwapManager(model.Buf, termd.SwapPathFor(fp), fp, 2*time.Second)
		// 后台 tick 需要“回到 UI 线程”采集快照（避免与编辑产生数据竞争），
		// 通过投递 swapTickMsg 消息实现（p.Send 跨 goroutine 线程安全）。
		sw.SetRequest(func() { p.Send(core.SwapTickMsg{}) })
		// 任何内容变更都置位 dirty：仅一次原子写，开销可忽略。
		model.Buf.SetNotify(sw.MarkDirty)
		if err := sw.Start(); err != nil {
			// 无法写 .swp（如所在目录只读）：降级为不启用，不阻断编辑。
			fmt.Fprintf(os.Stderr, termd.T("警告: 无法创建交换文件 %s: %v\n"), termd.SwapPathFor(fp), err)
		} else {
			model.Swap = sw
		}
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "运行错误: %v\n", err)
		os.Exit(1)
	}
	// 正常退出：优雅停止后台写盘并删除 .swp（干净退出，无需恢复）。
	if model.Swap != nil {
		model.Swap.Stop()
	}
}
