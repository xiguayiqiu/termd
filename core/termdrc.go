package core

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"termd"
)

// TermdConfig 保存从 ~/.termdrc 读取的持久化用户配置。
// 其作用类似 .bashrc / .vimrc：每次启动时自动加载，使设置（如行号、光标闪烁）
// 永久生效，无需每次手动输入 :set 命令。
type TermdConfig struct {
	// LineNum 行号显示模式（与运行时 :set nu / :set rnu 一致）。
	LineNum termd.LineNumMode
	// Blink 硬件光标是否闪烁（对应 :set cursorblink / :set nocursorblink）。
	Blink bool
	// CursorShape 光标形状：block(块) / bar(竖线) / underline(下划线)
	CursorShape int
	// FileIcons 文件浏览器是否渲染 Nerd Font 图标（仅 Nerd Font 终端有效）。
	FileIcons bool
	// SmoothScroll vim 式行滚动是否启用（对应 :set smoothscroll / :set nosmoothscroll）。
	SmoothScroll bool
}

// TermdrcName 是配置文件名（点文件，位于用户家目录）。
// Linux 与 Unix 统一使用该名称；Windows 下同样放在用户目录，保持跨平台一致。
const TermdrcName = ".termdrc"

// LoadTermdrc 读取并解析用户家目录下的 .termdrc。
// 若文件不存在或无法读取，返回带默认值的配置（行号关闭、光标闪烁开启），
// 不会报错中断程序启动。
//
// 支持的语法（每行一条，忽略空行与 # 注释）：
//
//	set nu              绝对行号（等价 :set nu）
//	set number
//	set rnu             相对行号（等价 :set rnu）
//	set relativenumber
//	set nonu            关闭行号
//	set numberone / set norelativenumber
//	set cursorblink     开启光标闪烁
//	set nocursorblink   关闭光标闪烁
//	set cursor block    光标形状：块（默认）
//	set cursor bar      光标形状：竖线
//	set cursor underline 光标形状：下划线
//	set fileicons       开启文件浏览器 Nerd Font 图标（需 Nerd Font 终端）
//	set nofileicons     关闭文件浏览器图标（普通终端默认）
//	set smoothscroll    开启 vim 式平滑滚动
//	set nosmoothscroll  关闭平滑滚动
//
// “set ”前缀可省略，直接写 “nu” 亦可。
func LoadTermdrc() *TermdConfig {
	cfg := &TermdConfig{
		LineNum:      termd.LNNone,
		Blink:        true,  // 与运行时默认一致
		CursorShape:  0,     // 0=block(块), 1=underline(下划线), 2=bar(竖线)
		FileIcons:    false, // 默认关闭：普通终端无 Nerd Font 字形，避免方块乱码
		SmoothScroll: true,  // 与运行时默认一致
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}
	path := filepath.Join(home, TermdrcName)
	f, err := os.Open(path)
	if err != nil {
		return cfg // 文件不存在：使用默认值
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "set ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "set "))
		}
		switch line {
		case "nu", "number":
			cfg.LineNum = termd.LNAbs
		case "rnu", "relativenumber":
			cfg.LineNum = termd.LNRel
		case "nonu", "nonumber", "norelativenumber":
			cfg.LineNum = termd.LNNone
		case "cursorblink":
			cfg.Blink = true
		case "nocursorblink":
			cfg.Blink = false
		case "cursor block":
			cfg.CursorShape = 0
		case "cursor bar":
			cfg.CursorShape = 2
		case "cursor underline":
			cfg.CursorShape = 1
		case "fileicons":
			cfg.FileIcons = true
		case "nofileicons":
			cfg.FileIcons = false
		case "smoothscroll":
			cfg.SmoothScroll = true
		case "nosmoothscroll":
			cfg.SmoothScroll = false
		}
	}
	return cfg
}

// ApplyTo 将配置应用到编辑器模型，使启动时的持久设置生效。
func (c *TermdConfig) ApplyTo(m *EditorModel) {
	m.sm.SetLineNum(c.LineNum)
	m.blinkMode = c.Blink
	m.cursorShape = c.CursorShape
	m.smoothScroll = c.SmoothScroll
	// 文件浏览器图标开关（Nerd Font 终端才应开启）
	termd.FBUseIcons = c.FileIcons
}
