package main

import (
	"bytes"
	"errors"
	"os"
)

// Buffer 是编辑器的内存行缓存核心。
// 设计目标：
//   - 使用 [][]byte 按行存储文件内容，保证 O(1) 的行级增删改；
//   - 维护 IsDirty 脏标记，仅在内容真正变更时才触发重绘，降低输入延迟；
//   - 内建一个简单的操作历史栈（undo stack），用于 :u 撤销。
type Buffer struct {
	// lines 每行内容（不包含末尾换行符）
	lines [][]byte
	// IsDirty 标记内容是否被修改过（决定是否需要重绘/是否允许 :q 未保存退出）
	IsDirty bool
	// filePath 关联的磁盘文件路径（为空表示新建未命名文件）
	filePath string
	// undoStack 操作历史栈，每个元素是一份完整的行快照
	undoStack [][][]byte
	// notify 是可选的变更回调（崩溃恢复用）：任何内容变更后触发一次，
	// 由 SwapManager 通过 MarkDirty 让后台线程节流写盘。因在 bubbletea
	// 单线程 Update 内同步调用，开销极小，绝不在回调里做重活。
	notify func()
}

// NewBuffer 创建一个空缓冲区。
func NewBuffer() *Buffer {
	return &Buffer{
		lines:     make([][]byte, 1), // 至少包含一行空行，便于立即输入
		IsDirty:   false,
		undoStack: make([][][]byte, 0, 64),
	}
}

// LoadFile 从磁盘读取文件内容填充缓冲区。
// 文件不存在时返回空缓冲区（视为新建文件），不视为错误。
func (b *Buffer) LoadFile(path string) error {
	b.filePath = path
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在 => 视为新建，初始化为一行空内容即可
			b.lines = make([][]byte, 1)
			b.IsDirty = false
			return nil
		}
		return err
	}
	b.lines = bytes.Split(data, []byte{'\n'})
	// 去掉可能的末尾空行噪音（当文件以 \n 结尾时 Split 会多出一个空切片）
	if len(b.lines) > 0 && len(b.lines[len(b.lines)-1]) == 0 {
		b.lines = b.lines[:len(b.lines)-1]
	}
	if len(b.lines) == 0 {
		b.lines = make([][]byte, 1)
	}
	b.IsDirty = false
	return nil
}

// SwapSnapshot 返回缓冲区全部行的深拷贝快照（供 SwapManager 在 UI 线程采集后写盘）。
// 返回的切片与内部 lines 完全独立，改动它不影响编辑缓冲区，故可在采集后安全交给后台线程。
func (b *Buffer) SwapSnapshot() [][]byte {
	return b.snapshot()
}

// FilePath 返回关联的磁盘文件路径（为空表示未命名缓冲区）。
func (b *Buffer) FilePath() string {
	return b.filePath
}

// SetNotify 注入内容变更回调（崩溃恢复用），返回原先的回调以便恢复。
func (b *Buffer) SetNotify(fn func()) {
	b.notify = fn
}

// markChanged 在每次内容变更后调用：置脏位并触发可选的回调。
// 回调只应做轻量工作（如置位 SwapManager 的 dirty 标志），严禁在此做磁盘 IO。
func (b *Buffer) markChanged() {
	b.IsDirty = true
	if b.notify != nil {
		b.notify()
	}
}

// writeTo 将缓冲区内容序列化后写入指定路径。
// 若目标文件不存在则自动创建（包括多级目录由调用方保证存在），符合“保存即创建”语义。
func (b *Buffer) writeTo(path string) error {
	var buf bytes.Buffer
	for i, line := range b.lines {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(line)
	}
	// os.WriteFile 在文件不存在时会创建它（权限 0o644）；已存在则覆盖写入。
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return nil
}

// Save 将缓冲区内容写回磁盘。
//   - filePath 已指定（即使磁盘上尚不存在该文件）：直接创建/覆盖写入。
//   - filePath 为空（启动时未给文件名）：返回提示，要求用 :w 文件名 指定。
//
// force 参数预留用于忽略只读等限制（此处仅作语义占位）。
func (b *Buffer) Save(force bool) error {
	if b.filePath == "" {
		return errors.New("未指定文件名，无法保存（请使用 :w 文件名 创建，或先通过 :ex 选择文件）")
	}
	if err := b.writeTo(b.filePath); err != nil {
		return err
	}
	b.IsDirty = false
	return nil
}

// SaveAs 以指定文件名保存（创建新文件或覆盖），并把它设为当前关联文件。
// 用于命令 :w 文件名 / :w! 文件名 / 另存为场景。
func (b *Buffer) SaveAs(path string) error {
	if path == "" {
		return errors.New("文件名不能为空")
	}
	if err := b.writeTo(path); err != nil {
		return err
	}
	b.filePath = path
	b.IsDirty = false
	return nil
}

// LineCount 返回总行数。
func (b *Buffer) LineCount() int { return len(b.lines) }

// GetLine 获取指定行（越界返回空切片）。
func (b *Buffer) GetLine(row int) []byte {
	if row < 0 || row >= len(b.lines) {
		return nil
	}
	return b.lines[row]
}

// snapshot 拷贝当前所有行，用于撤销栈。
func (b *Buffer) snapshot() [][]byte {
	cp := make([][]byte, len(b.lines))
	for i, ln := range b.lines {
		row := make([]byte, len(ln))
		copy(row, ln)
		cp[i] = row
	}
	return cp
}

// pushUndo 在每次“破坏性编辑”前调用，压入一份快照。
func (b *Buffer) pushUndo() {
	b.undoStack = append(b.undoStack, b.snapshot())
	// 限制栈深度，避免无限增长占用内存
	if len(b.undoStack) > 200 {
		b.undoStack = b.undoStack[1:]
	}
}

// Undo 撤销上一步编辑，恢复最近一次快照。
func (b *Buffer) Undo() bool {
	if len(b.undoStack) == 0 {
		return false
	}
	last := b.undoStack[len(b.undoStack)-1]
	b.undoStack = b.undoStack[:len(b.undoStack)-1]
	b.lines = last
	b.markChanged()
	return true
}

// InsertRune 在 (row, col) 处插入一个 rune。越界自动钳制。
func (b *Buffer) InsertRune(row, col int, r rune) {
	if row < 0 || row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	line := b.lines[row]
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	b.pushUndo()
	// 在 col 处插入 r，UTF-8 编码后写入
	newline := make([]byte, 0, len(line)+4)
	newline = append(newline, line[:col]...)
	var rb [4]byte
	n := encodeRune(r, rb[:])
	newline = append(newline, rb[:n]...)
	newline = append(newline, line[col:]...)
	b.lines[row] = newline
	b.markChanged()
}

// DeleteRune 删除 (row, col) 处的字符（向后删除）。返回是否真删除了内容。
func (b *Buffer) DeleteRune(row, col int) bool {
	if row < 0 || row >= len(b.lines) {
		return false
	}
	line := b.lines[row]
	if col < 0 || col >= len(line) {
		return false
	}
	b.pushUndo()
	// 计算该位置第一个 rune 的字节长度，按 rune 删除而非按字节
	_, size := decodeRune(line[col:])
	b.lines[row] = append(line[:col], line[col+size:]...)
	b.markChanged()
	return true
}

// Backspace 在 (row, col) 处向前删除一个字符。
// 若 col==0 且不在首行，则合并到上一行（行拼接）。
func (b *Buffer) Backspace(row, col int) bool {
	if row < 0 || row >= len(b.lines) {
		return false
	}
	line := b.lines[row]
	// 钳制字节列，避免光标越界；光标最多在行尾（col==len(line)）
	if col > len(line) {
		col = len(line)
	}
	if col > 0 {
		b.pushUndo()
		// 找到待删除 rune 的起点（处理 UTF-8 多字节字符）。
		// 光标位于 col（rune 边界），待删除字符区间为 [start, col)：从 col-1 向左回溯
		// 到首个“rune 首字节”（ASCII 为单字节起点；多字节字符回溯到其首字节）。
		// 修复：原先 start 从 col 起回溯但检查的是 line[col]（光标右侧字符），当其为
		// ASCII 时条件直接为假、start 停在 col，导致删除空区间、内容纹丝不动。
		start := col - 1
		for start > 0 && !utf8RuneStart(line[start]) {
			start--
		}
		b.lines[row] = append(line[:start], line[col:]...)
		b.markChanged()
		return true
	}
	// 行首退格 => 合并到上一行
	if row > 0 {
		b.pushUndo()
		prev := b.lines[row-1]
		cur := b.lines[row]
		merged := make([]byte, 0, len(prev)+len(cur))
		merged = append(merged, prev...)
		merged = append(merged, cur...)
		b.lines[row-1] = merged
		b.lines = append(b.lines[:row], b.lines[row+1:]...)
		b.markChanged()
		return true
	}
	return false
}

// InsertNewline 在 (row, col) 处换行：当前行拆分为两行。
func (b *Buffer) InsertNewline(row, col int) (int, int) {
	if row < 0 || row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	line := b.lines[row]
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	b.pushUndo()
	head := make([]byte, col)
	copy(head, line[:col])
	tail := make([]byte, len(line)-col)
	copy(tail, line[col:])
	b.lines[row] = head
	b.lines = append(b.lines[:row+1], append([][]byte{tail}, b.lines[row+1:]...)...)
	b.markChanged()
	return row + 1, 0
}

// SetLine 整行替换（搜索替换 / 外部修改时使用）。
func (b *Buffer) SetLine(row int, content []byte) {
	if row < 0 || row >= len(b.lines) {
		return
	}
	b.pushUndo()
	b.lines[row] = append([]byte(nil), content...)
	b.markChanged()
}

// DeleteLine 删除指定行（用于 vim 'dd'）。保证删除后至少保留一行空行。
func (b *Buffer) DeleteLine(row int) {
	if row < 0 || row >= len(b.lines) {
		return
	}
	b.pushUndo()
	b.lines = append(b.lines[:row], b.lines[row+1:]...)
	if len(b.lines) == 0 {
		b.lines = make([][]byte, 1)
	}
	b.markChanged()
}

// OpenLineBelow 在 row 行之后插入一个空行，返回新空行的索引（仿 vim 'o'）。
func (b *Buffer) OpenLineBelow(row int) int {
	if row < 0 || row >= len(b.lines) {
		row = len(b.lines) - 1
	}
	b.pushUndo()
	tail := make([][]byte, len(b.lines[row+1:]))
	copy(tail, b.lines[row+1:])
	b.lines = append(b.lines[:row+1], append([][]byte{{}}, tail...)...)
	b.markChanged()
	return row + 1
}

// OpenLineAbove 在 row 行之前插入一个空行，返回新空行的索引（仿 vim 'O'）。
func (b *Buffer) OpenLineAbove(row int) int {
	if row < 0 || row >= len(b.lines) {
		row = 0
	}
	b.pushUndo()
	tail := make([][]byte, len(b.lines[row:]))
	copy(tail, b.lines[row:])
	b.lines = append(b.lines[:row], append([][]byte{{}}, tail...)...)
	b.markChanged()
	return row
}
