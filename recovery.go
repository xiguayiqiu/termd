package termd

// ============================================================
// recovery —— 崩溃恢复 / 数据持久化的底层工具
// ============================================================
//
// 借鉴 vim 源码的思想（src/memline.c 的 .swp 交换文件、src/bufwrite.c 的安全写入）：
//   - 交换文件（Swap）：编辑期间把内存脏数据周期写入同目录下的 ".swp" 文件，
//     崩溃后可用其恢复未保存的改动；
//   - 原子写入（Atomic save）：先写临时文件再 os.Rename，保证原文件在任意时刻
//     要么是旧内容、要么是新内容，绝不出现写一半损坏的中间态；
//   - PID / 时间戳元数据：启动时读 swap 元数据判断是“正常退出 / 崩溃 / 被其他
//     编辑器占用”。
//
// 本文件提供不依赖并发的底层能力（元数据编解码、原子写、崩溃状态判定），
// 高并发的后台写盘编排见 swap.go 的 SwapManager。

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// swapMagic 是 .swp 文件的魔数头（第一行），用于快速识别与丢弃异类文件。
const swapMagic = "TERMD-SWAP-v1"

// appName 写入 swap 元数据，标识由哪个编辑器生成（类似 vim 的 VIM8.3 …）。
const appName = "termd"

// SwapMeta 是 .swp 文件头部的元数据（JSON 行），存于魔数行之后。
// 用于崩溃检测（PID 是否存活）与恢复提示（时间戳、原文件路径）。
type SwapMeta struct {
	App     string `json:"app"`   // 生成者：termd
	Pid     int    `json:"pid"`   // 生成该 swap 的进程 PID
	Time    int64  `json:"time"`  // 最近一次写入的 Unix 秒
	File    string `json:"file"`  // 关联的正文文件路径（绝对路径）
	LineCnt int    `json:"lines"` // 快照行数
	BodyCRC uint32 `json:"crc"`   // 正文字节流的 CRC32，用于损坏检测
}

// swapBody 是 swap 文件的正文（gob 编码），存于元数据行之后。
// 用 gob 而非文本拼接，可无损承载任意字节（含二进制/CJK）。
type swapBody struct {
	Lines [][]byte
}

// newSwapMeta 构造当前时刻、当前 PID 的元数据。
func newSwapMeta(filePath string, lineCnt int) SwapMeta {
	return SwapMeta{
		App:     appName,
		Pid:     os.Getpid(),
		Time:    time.Now().Unix(),
		File:    filePath,
		LineCnt: lineCnt,
	}
}

// encodeBody 将行集编码为 gob 字节流，并返回其 CRC32。
func encodeBody(lines [][]byte) ([]byte, uint32, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(swapBody{Lines: lines}); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), crc32.ChecksumIEEE(buf.Bytes()), nil
}

// encodeSwapFile 组装完整 swap 文件内容：魔数行 + 元数据 JSON 行 + 正文字节流。
func encodeSwapFile(meta SwapMeta, lines [][]byte) ([]byte, error) {
	body, crc, err := encodeBody(lines)
	if err != nil {
		return nil, err
	}
	meta.LineCnt = len(lines)
	meta.BodyCRC = crc
	mj, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(swapMagic + "\n")
	out.Write(mj)
	out.WriteString("\n")
	out.Write(body)
	return out.Bytes(), nil
}

// decodeSwapFile 解析 swap 文件内容，返回元数据与恢复出的行集。
// 校验魔数、行数、CRC，任一不符即报错（视为损坏）。
func decodeSwapFile(data []byte) (*SwapMeta, [][]byte, error) {
	nl1 := bytes.IndexByte(data, '\n')
	if nl1 < 0 {
		return nil, nil, errors.New("swap: 缺少魔数行")
	}
	if string(data[:nl1]) != swapMagic {
		return nil, nil, errors.New("swap: 魔数不匹配（非 termd 交换文件）")
	}
	rest := data[nl1+1:]
	nl2 := bytes.IndexByte(rest, '\n')
	if nl2 < 0 {
		return nil, nil, errors.New("swap: 缺少元数据行")
	}
	var meta SwapMeta
	if err := json.Unmarshal(rest[:nl2], &meta); err != nil {
		return nil, nil, fmt.Errorf("swap: 元数据解析失败: %w", err)
	}
	body := rest[nl2+1:]
	if crc32.ChecksumIEEE(body) != meta.BodyCRC {
		return nil, nil, errors.New("swap: 正文字节流校验失败（CRC 不匹配，文件可能损坏）")
	}
	var sb swapBody
	if err := gob.NewDecoder(bytes.NewReader(body)).Decode(&sb); err != nil {
		return nil, nil, fmt.Errorf("swap: 正文字节流解码失败: %w", err)
	}
	if len(sb.Lines) != meta.LineCnt {
		return nil, nil, errors.New("swap: 行数与元数据不一致")
	}
	return &meta, sb.Lines, nil
}

// WriteSwapFile 将行集原子写入 swap 文件。swp 须为绝对路径。
// 即便写盘中途断电/崩溃，旧 swap 也不会被破坏（os.Rename 保证）。
func WriteSwapFile(swp string, lines [][]byte, meta SwapMeta) error {
	data, err := encodeSwapFile(meta, lines)
	if err != nil {
		return err
	}
	return AtomicWrite(swp, data, 0o644)
}

// ReadSwapFile 从 swap 文件读取元数据与行集。
func ReadSwapFile(swp string) (*SwapMeta, [][]byte, error) {
	data, err := os.ReadFile(swp)
	if err != nil {
		return nil, nil, err
	}
	return decodeSwapFile(data)
}

// RemoveSwapFile 删除 swap 文件（正常退出时调用，清除恢复痕迹）。
// 文件不存在时静默返回 nil。
func RemoveSwapFile(swp string) error {
	if err := os.Remove(swp); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AtomicWrite 原子写入：在目标同目录写临时文件 -> fsync -> os.Rename 覆盖目标
// -> fsync 目录。任一步失败都清理临时文件并返回错误，保证原文件不被写坏。
// 返回后目标文件要么是旧内容、要么是全新内容，绝无“半个文件”。
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	// 临时文件必须与目标同目录，否则 os.Rename 会跨文件系统失败。
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 显式清理：任何失败路径都删除临时文件。
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// 先落盘（fsync），再 rename，确保数据而非仅元数据已持久化。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err // rename 失败时临时文件仍在，交给 defer 清理
	}
	tmpName = "" // rename 成功，不再清理
	// fsync 目录，让 rename 本身也持久化（否则断电后目录项可能回滚）。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// SwapState 描述启动时发现的 swap 文件状态。
type SwapState int

const (
	// SwapNone 不存在 swap 文件 => 上次正常退出（或从无编辑）。
	SwapNone SwapState = iota
	// SwapStale 存在 swap 且 PID 已不存活 => 上次异常退出（崩溃），可恢复。
	SwapStale
	// SwapActive 存在 swap 且 PID 仍存活 => 另一编辑器实例正在编辑，勿覆盖。
	SwapActive
	// SwapSelf PID 等于当前进程 => 理论上不该出现（同一进程只建一个管理器）。
	SwapSelf
	// SwapCorrupt 文件无法解析/CRC 校验失败 => 损坏，仅提示不可恢复。
	SwapCorrupt
)

// aliveFunc 判断 pid 进程是否仍存活；nil 时用默认实现（signal 0 探测）。
type aliveFunc func(pid int) bool

// processAlive 默认存活检测：向 pid 发送信号 0（不实际发信号，仅探测）。
// 权限不足被视为存活（保守，避免误删正在编辑的文件）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// CheckSwapState 在启动时判断是否存在可恢复的崩溃现场。
//   - swapPath：.swp 文件绝对路径；
//   - myPid：当前进程 PID；
//   - alive：进程存活探针（测试可注入）。
//
// 返回状态、元数据（如有且可解析）、错误（仅文件系统错误时非 nil）。
func CheckSwapState(swapPath string, myPid int, alive aliveFunc) (SwapState, *SwapMeta, error) {
	if alive == nil {
		alive = processAlive
	}
	if _, err := os.Stat(swapPath); err != nil {
		if os.IsNotExist(err) {
			return SwapNone, nil, nil
		}
		return SwapNone, nil, err
	}
	meta, _, err := ReadSwapFile(swapPath)
	if err != nil {
		// 存在但解析失败 => 损坏。
		return SwapCorrupt, nil, nil
	}
	switch {
	case meta.Pid == myPid:
		return SwapSelf, meta, nil
	case alive(meta.Pid):
		return SwapActive, meta, nil
	default:
		return SwapStale, meta, nil
	}
}

// RecoverySummary 生成给用户看的恢复提示语（配合状态机多选）。
func RecoverySummary(st SwapState, meta *SwapMeta) string {
	switch st {
	case SwapStale:
		if meta == nil {
			return "检测到上次异常退出留下的交换文件，但无法解析其内容"
		}
		t := time.Unix(meta.Time, 0).Format("2006-01-02 15:04:05")
		return fmt.Sprintf("检测到崩溃现场（%s 于 %s，PID %d，共 %d 行）。可恢复未保存的编辑内容。",
			swapBaseName(meta.File), t, meta.Pid, meta.LineCnt)
	case SwapActive:
		return "检测到另一编辑器实例正在编辑此文件，已进入只读保护，避免相互覆盖"
	case SwapCorrupt:
		return "检测到交换文件但内容损坏，可能无法完整恢复"
	default:
		return ""
	}
}

// swapBaseName 仅用于提示：返回文件名或“未命名”。
func swapBaseName(full string) string {
	if full == "" {
		return "未命名缓冲区"
	}
	if i := strings.LastIndexByte(full, '/'); i >= 0 {
		return full[i+1:]
	}
	return full
}
