package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/muesli/termenv"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// ============================================================
// 图片加载与渲染
// ============================================================
//
// 目标：让 Preview 模式能够「网络加载」并「渲染」Markdown 中的图片：
//   - 支持 http(s):// 远程图片（带本地缓存，避免重复下载）
//   - 支持本地相对/绝对路径图片
//   - 支持位图（PNG/JPEG/GIF）与矢量图（SVG，纯 Go 光栅化）
//   - 渲染采用「半块字符 + 真彩」方案（▀ 上色），不依赖 Kitty/Sixel/iTerm
//     等特定终端协议，在任何支持真彩的终端中都能显示。

// reStandaloneImage 匹配独占一行的图片语法 ![alt](url "title")。
// 允许 url 后带可选 "title"（含空格），结尾为 )。
var reStandaloneImage = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)]+?)(?:\s+"[^"]*")?\)$`)

// reImageInLink 匹配 [![alt](url "title")](link) 形式（图片外面包一层链接）。
var reImageInLink = regexp.MustCompile(`^\[!\[([^\]]*)\]\(([^)]+?)(?:\s+"[^"]*")?\)\]\(([^)]+)\)$`)

// imgHTTPClient 带超时的 HTTP 客户端，避免网络图片加载阻塞 UI 过久。
var imgHTTPClient = &http.Client{Timeout: 10 * time.Second}

// imgCellAspect 终端单元格「高/宽」比例，用于计算图片渲染的纵向行数，保证 1:1 不压缩。
//   - 取值 1.0：每个单元格纵向代表 1 个源像素（1:1，适合单元格接近正方形或希望图片不被纵向压扁的终端）。
//   - 取值 2.0：经典终端（单元格高约为宽 2 倍）下用半块 ▀ 时的正确比例。
// 若你的预览图片看起来被纵向压扁，将它调小（如 1.0）；若被纵向拉伸，调大（如 2.0）。
var imgCellAspect float64 = 1.0

// imgCacheDir 下载图片的本地缓存目录（懒初始化）。
var imgCacheDir string

// imgMemCache 内存缓存：src -> 解码后的图像（避免重复解码）。
var imgMemCache = map[string]image.Image{}

// imgLoading 标记正在后台加载的远程图片 src，避免每次重绘都启动重复 goroutine。
var imgLoading = map[string]bool{}

// ============================================================
// 终端图形协议（Kitty / Sixel）：原生分辨率渲染，避免低清马赛克
// ============================================================
//
// 当终端支持图形协议时，直接上传图像数据，由终端按单元格区域缩放显示，
// 画质为图片原生分辨率（清晰，而非半块字符的低分模拟）。

// imgRenderMode 控制图片渲染方式：
//   - "auto"     ：自动探测，支持 Kitty 则用 Kitty，否则回退半块字符（默认）
//   - "kitty"    ：强制使用 Kitty 图形协议
//   - "halfblock"：强制使用半块字符回退（任意真彩终端可用）
var imgRenderMode = "auto"

// imgUseKitty 根据渲染模式与终端能力判断是否使用 Kitty 图形协议。
func imgUseKitty() bool {
	if imgRenderMode == "halfblock" {
		return false
	}
	if imgRenderMode == "kitty" {
		return true
	}
	return kittySupported()
}

// kittySupported 通过环境变量探测终端是否支持 Kitty 图形协议。
func kittySupported() bool {
	tp := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	switch tp {
	case "kitty", "wezterm", "ghostty":
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	for _, s := range []string{"kitty", "wezterm", "ghostty"} {
		if strings.Contains(term, s) {
			return true
		}
	}
	// VTE 系（gnome-terminal 等）新版也支持 Kitty 协议，但无法可靠探测；
	// 这里仅对已知稳定支持的终端开启，其余回退半块字符。
	return false
}

// kittyID 由图片源生成稳定的图片 id（0~2147483647 范围，Kitty 协议要求）。
func kittyID(src string) int {
	h := sha256.Sum256([]byte(src))
	id := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
	if id < 1 {
		id = 1
	}
	return id
}

// kittyPNG 将图像编码为 PNG 字节（Kitty 协议 f=100 即 PNG）。
func kittyPNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// kittyTransmitString 构造 Kitty 协议「上传」转义字符串（分块 base64）。
func kittyTransmitString(id int, data []byte) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	const chunk = 4096
	var sb strings.Builder
	for i := 0; i < len(b64); i += chunk {
		end := i + chunk
		if end > len(b64) {
			end = len(b64)
		}
		more := 0
		if end < len(b64) {
			more = 1
		}
		if i == 0 {
			sb.WriteString(fmt.Sprintf("\x1b_Gf=100,a=T,t=d,i=%d,m=%d;%s\x1b\\", id, more, b64[i:end]))
		} else {
			sb.WriteString(fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, b64[i:end]))
		}
	}
	return sb.String()
}

// renderImageKitty 使用 Kitty 图形协议渲染：每行对应图片的一行占位。
//   - 第 0 行：上传（每次重建都重新上传，确保 bubbletea 全屏重绘时图片数据始终在场）
//     + 放置（a=p），并带 C=1 使放置后光标不自动移动，完全交由我们显式的换行控制。
//   - 其余行：与图片等宽的空格占位（cols 个空格），占住图片在字符网格中的列宽，
//     避免重绘时光标列漂移导致图片错位。
// 返回 rows 行字符串（不含结尾换行），整体交由 bubbletea 的 View 统一输出。
func renderImageKitty(img image.Image, src string, cols, rows int) []string {
	id := kittyID(src)
	// 每次都重新上传（q=2 保留传输，便于重绘时复用）；去掉"只上传一次"的全局缓存，
	// 因为 bubbletea 每次全屏重绘都需要在当前光标网格位置重新放置图片。
	upload := kittyTransmitString(id, kittyPNG(img))
	// 放置：在光标处占据 cols 列、rows 行；C=1 表示放置后不移动光标（由我们显式换行）。
	place := fmt.Sprintf("\x1b_Gq=2,a=p,i=%d,c=%d,r=%d,C=1\x1b\\", id, cols, rows)

	lines := make([]string, rows)
	lines[0] = upload + place
	// 占位行：等宽空格（带重置），确保字符网格列宽 == 图片列宽
	pad := strings.Repeat(" ", cols) + "\x1b[0m"
	for y := 1; y < rows; y++ {
		lines[y] = pad
	}
	return lines
}

// ensureImgCacheDir 创建（必要时）图片缓存目录。
func ensureImgCacheDir() string {
	if imgCacheDir != "" {
		return imgCacheDir
	}
	dir, err := os.MkdirTemp("", "termd-images-")
	if err != nil {
		dir = os.TempDir()
	}
	imgCacheDir = dir
	return dir
}

// isRemote 判断 src 是否为网络地址。
func isRemote(src string) bool {
	return strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
}

// cacheKey 为图片源生成缓存文件名（SHA256 前缀）。
func cacheKey(src string) string {
	h := sha256.Sum256([]byte(src))
	return hex.EncodeToString(h[:])[:32]
}

// loadImageBytes 读取图片原始字节：远程走 HTTP（带缓存），本地直接读文件。
func loadImageBytes(src string) ([]byte, error) {
	if isRemote(src) {
		// 先查磁盘缓存
		cacheFile := filepath.Join(ensureImgCacheDir(), cacheKey(src))
		if data, err := os.ReadFile(cacheFile); err == nil {
			return data, nil
		}
		// 下载
	req, err := http.NewRequest(http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	// 部分图床（如 Wikimedia）要求带 User-Agent，否则返回 403
	req.Header.Set("User-Agent", "termd/1.0 (+https://github.com/termd/termd)")
		resp, err := imgHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		// 写入缓存（失败不影响返回）
		_ = os.WriteFile(cacheFile, data, 0o600)
		return data, nil
	}
	// 本地文件
	return os.ReadFile(src)
}

// decodeImage 将字节解码为 image.Image，自动识别位图与 SVG。
func decodeImage(data []byte) (image.Image, error) {
	// 快速判断是否 SVG（以 <?xml 或 <svg 开头）
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<svg")) {
		return rasterizeSVG(trimmed)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return img, nil
}

// rasterizeSVG 使用 oksvg + rasterx 将 SVG 光栅化为位图。
// 先以参考宽度 240 渲染（保持宽高比），下游再由 renderImageAsANSI 重采样到目标尺寸。
func rasterizeSVG(data []byte) (image.Image, error) {
	const refW = 240
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	vbW, vbH := icon.ViewBox.W, icon.ViewBox.H
	if vbW <= 0 || vbH <= 0 {
		vbW, vbH = float64(refW), float64(refW)
	}
	refH := int(refW * vbH / vbW)
	if refH <= 0 {
		refH = refW
	}
	icon.SetTarget(0, 0, float64(refW), float64(refH))
	rgba := image.NewRGBA(image.Rect(0, 0, refW, refH))
	// 透明背景填充白色，避免 PNG 透明处显示为黑块
	draw.Draw(rgba, rgba.Bounds(), image.White, image.Point{}, draw.Src)
	scanner := rasterx.NewScannerGV(refW, refH, rgba, rgba.Bounds())
	rast := rasterx.NewDasher(refW, refH, scanner)
	icon.Draw(rast, 0)
	return rgba, nil
}

// imgReadyMsg 在后台图片加载完成后由 tea.Send 发出，通知 model 重新渲染 Preview（异步、非阻塞）。
type imgReadyMsg struct{}

// IsImageLoading 报告某 src 是否正在后台加载中（远程图片未下载完时为真）。
// 供 Preview 文本占位区分「加载中」与「加载失败」。
func IsImageLoading(src string) bool {
	return imgLoading[src]
}

// LoadImageAsync 非阻塞地加载图片。
//   - 返回 (img, true) 表示已同步可用（命中内存缓存 / 本地文件 / 本次就已完成），调用方可立即渲染。
//   - 返回 (nil, false) 表示图片尚在后台加载（通常是远程 URL），调用方应改用轻量文本占位；
//     加载完成后会写入内存缓存并由 tea.Send(imgReadyMsg{}) 触发一次重绘，下一次渲染即可拿到图片。
// 这样 Preview 渲染在 View() 中绝不会因网络下载而阻塞主事件循环（避免光标冻结/卡死）。
// LoadImageAsync 非阻塞地加载图片。
//   - 返回 (img, true) 表示已同步可用（命中内存缓存 / 本地文件 / 本次就已完成），调用方可立即渲染。
//   - 返回 (nil, false) 表示图片尚在后台加载（通常是远程 URL），调用方应改用轻量文本占位；
//     加载完成后会写入内存缓存并调用 notify() 触发一次重绘，下一次渲染即可拿到图片。
// notify 由调用方传入（通常注入 bubbletea 的 Program.Send），用于后台完成时通知重绘；可为 nil。
// 这样 Preview 渲染在 View() 中绝不会因网络下载而阻塞主事件循环（避免光标冻结/卡死）。
func LoadImageAsync(src string, notify func()) (image.Image, bool) {
	if img, ok := imgMemCache[src]; ok {
		return img, true
	}
	// 本地文件同步加载（磁盘读取足够快，无需异步）
	if !isRemote(src) {
		data, err := loadImageBytes(src)
		if err != nil {
			return nil, false
		}
		img, err := decodeImage(data)
		if err != nil {
			return nil, false
		}
		imgMemCache[src] = img
		return img, true
	}
	// 远程图片：后台异步下载，避免阻塞 UI
	if !imgLoading[src] {
		imgLoading[src] = true
		go func() {
			data, err := loadImageBytes(src)
			if err == nil {
				if img, derr := decodeImage(data); derr == nil {
					imgMemCache[src] = img
				}
			}
			delete(imgLoading, src)
			// 通知 model 重绘 Preview（notify 内部通过 bubbletea 线程安全地注入消息）
			if notify != nil {
				notify()
			}
		}()
	}
	return nil, false
}

// renderImageLines 将图像渲染为「每行一个字符串」的切片（不含结尾换行），
// 用于与 bubbletea 的分行输出 / 行号前缀拼接对齐。
//   - 优先使用终端图形协议（Kitty）以「原生分辨率」呈现，避免低清马赛克；
//     不支持时回退为「半块字符 + 真彩」方案。
//   - cols：输出宽度（以终端单元格计）。
//   - profile：终端色彩能力（仅半块回退路径使用）。
//   - 返回的切片长度 == rows：首行为图形本体（Kitty 放置序列 / 半块首行），
//     其余行是与图片几何等宽的「占位行」（空格），用于占住图片在字符网格中的
//     高度与列宽，保证 bubbletea 全屏重绘时光标列始终可预测，避免图片错位。
func renderImageLines(src string, cols int, profile termenv.Profile, notify func()) []string {
	if cols < 2 {
		cols = 2
	}
	img, ready := LoadImageAsync(src, notify)
	if !ready || img == nil {
		// 异步加载中或加载失败：返回空切片，由调用方回退为轻量文本占位（不阻塞 UI）
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	// 输出行数：每终端单元格纵向代表 imgCellAspect 个源像素（半块 ▀ 一次画 2 个像素，
	// 这里以 imgCellAspect 归一化，默认 1.0 表示 1:1 不压缩）。用浮点计算避免类型错误。
	rows := int(float64(cols) * float64(h) / (float64(w) * imgCellAspect))
	if rows < 1 {
		rows = 1
	}
	const maxRows = 40
	if rows > maxRows {
		rows = maxRows
		cols = int(float64(maxRows) * float64(w) / float64(h) * imgCellAspect) // 等比收窄宽度
		if cols < 2 {
			cols = 2
		}
	}

	// 优先尝试 Kitty 图形协议（原生分辨率，清晰）
	if imgUseKitty() {
		return renderImageKitty(img, src, cols, rows)
	}
	// 回退：半块字符 + 真彩
	return renderImageHalfBlock(img, cols, rows, profile)
}

// renderImageHalfBlock 「半块字符 + 真彩」回退渲染（低分辨率，仅在不支持图形协议时使用）。
// 返回 rows 行，每行不含结尾换行；首行为完整半块字符画，其余行复用最后一行颜色并
// 以等宽空格占位，确保列宽与图片一致。
func renderImageHalfBlock(img image.Image, cols, rows int, profile termenv.Profile) []string {
	// 采样到 (cols, 2*rows) 的像素网格
	pixW, pixH := cols, 2*rows
	grid := sampleImage(img, pixW, pixH)

	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		var sb strings.Builder
		for x := 0; x < cols; x++ {
			top := grid[y*2][x]
			bot := grid[y*2+1][x]
			sb.WriteString(cell(top, bot, profile))
		}
		sb.WriteString("\x1b[0m")
		lines[y] = sb.String()
	}
	return lines
}

// sampleImage 用盒式平均将图像重采样到 (w, h) 网格，返回每格平均色。
func sampleImage(img image.Image, w, h int) [][]color.Color {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw <= 0 || ih <= 0 {
		return nil
	}
	grid := make([][]color.Color, h)
	for y := 0; y < h; y++ {
		grid[y] = make([]color.Color, w)
		for x := 0; x < w; x++ {
			// 源矩形（浮点 -> 整数区间）
			x0 := b.Min.X + x*iw/w
			x1 := b.Min.X + (x+1)*iw/w
			y0 := b.Min.Y + y*ih/h
			y1 := b.Min.Y + (y+1)*ih/h
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var r, g, bl, a uint32
			var n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					rr, gg, bb, aa := img.At(sx, sy).RGBA()
					r += rr
					g += gg
					bl += bb
					a += aa
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			grid[y][x] = color.RGBA64{
				R: uint16(r / n),
				G: uint16(g / n),
				B: uint16(bl / n),
				A: uint16(a / n),
			}
		}
	}
	return grid
}

// cell 生成单个半块字符：前景=上像素色，背景=下像素色。
func cell(top, bot color.Color, profile termenv.Profile) string {
	tr, tg, tb, _ := toRGB(top)
	br, bg, bb, _ := toRGB(bot)
	if profile == termenv.TrueColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
	}
	// 非真彩：退化为 256 色
	return fmt.Sprintf("\x1b[38;5;%dm\x1b[48;5;%dm▀", ansi256(tr, tg, tb), ansi256(br, bg, bb))
}

// toRGB 将 color.Color 转为 0-255 的 RGB。
func toRGB(c color.Color) (r, g, b, a uint32) {
	rr, gg, bb, aa := c.RGBA()
	return rr >> 8, gg >> 8, bb >> 8, aa >> 8
}

// ansi256 将 RGB 量化到 256 色（6x6x6 立方体 + 灰度阶梯）。
func ansi256(r, g, b uint32) int {
	rc, gc, bc := float64(r)/255, float64(g)/255, float64(b)/255
	// 灰度检测
	if rc == gc && gc == bc {
		v := int(rc * 25)
		if v == 0 {
			return 16
		}
		return 232 + v
	}
	ri := int(rc*5 + 0.5)
	gi := int(gc*5 + 0.5)
	bi := int(bc*5 + 0.5)
	return 16 + 36*ri + 6*gi + bi
}

// extractBlockImage 从独占一行的文本中提取图片 (alt, url)。
// 支持 ![alt](url) 与 [![alt](url)](link) 两种形式。
func extractBlockImage(trimmed string) (alt, url string, ok bool) {
	if m := reStandaloneImage.FindStringSubmatch(trimmed); m != nil {
		return m[1], m[2], true
	}
	if m := reImageInLink.FindStringSubmatch(trimmed); m != nil {
		return m[1], m[2], true
	}
	return "", "", false
}
