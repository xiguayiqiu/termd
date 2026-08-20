# model.go 拆分说明（大重构适配版）

## 背景

`model.go`（原约 3000+ 行）已按功能拆分为多个 `model_*.go` 文件，全部位于 `core/` 目录。

## 当前布局（最终定稿）

项目为 **Go 多包结构**：

```
termd/
├── cmd/termd/main.go     ← package main（程序入口）
├── core/                 ← package core（编辑器模型层，import 路径 termd/core）
│   ├── model_*.go        ← 10 个拆分文件（model_types/update/edit/preview/command/…）
│   ├── mouse.go          ← 与 editorModel 紧耦合，随模型同包
│   ├── scroll_render.go  ← 视觉行滚动（依赖 editorModel）
│   ├── outline.go        ← 大纲侧边栏（依赖 editorModel）
│   ├── statusbar.go      ← 状态栏/命令栏（依赖 editorModel）
│   └── termdrc.go        ← ~/.termdrc 解析（依赖 editorModel）
├── *.go                  ← package termd（基础组件：buffer/renderer/statemachine/keymap/i18n/swap/…）
├── go.mod / go.sum
└── README.md
```

- 构建命令：`go build -o termd ./cmd/termd`（旧命令 `go build -o termd ./core` 已失效）。
- 运行：`go run ./cmd/termd`。

## 为什么这样拆分

Go 的 package 边界以目录为界，**同一目录的文件才共享未导出符号**，且 `main` 包不能被 import。`model_*.go` 与根目录基础组件（`Buffer`/`Renderer`/`StateMachine` 等）存在**双向依赖**（model 引用渲染/缓冲区，而 `mouse.go`/`scroll_render.go`/`outline.go`/`statusbar.go`/`termdrc.go` 又引用 `editorModel`）。

为把 `model_*.go` 独立成 `core/` 包并保证可编译，采用**大重构适配**：

1. **包划分**：`core/` 含 10 个 `model_*.go` + 5 个与 `editorModel` 紧耦合的文件；根目录 `package termd` 含其余 12 个基础组件文件；`main.go` 移到 `cmd/termd/` 作为 `package main`。
2. **单向依赖**：`core` → 根目录（`termd`）单向引用，根目录不反向引用 `core`，避免 import cycle。
3. **跨包符号导出**：所有被跨包引用的未导出符号改名导出（首字母大写），如：
   - `termd` 包：`WrapText`/`FBDisplayWidth`/`DecodeRune`/`LineNumMode`/`LNNone`/`LNRel`/`LNAbs`/`EditInsert`/`EditNormal`/`ModeNames`/`IsFenceStart`…（原 `wrapText`/`fbDisplayWidth`/…）
   - `core` 包：`EditorModel`（原 `editorModel`）、`NewModel`（原 `newEditorModel`）、`SwapTickMsg`（原 `swapTickMsg`）、`TermdrcName`（原 `termdrcName`），及 `Buf`/`Rend`/`SendMsg`/`Swap` 四个导出字段。
   - 导出字段（原未导出、被 core 访问）：`Buffer.Lines`、`Renderer.Styles`、`FileBrowser.Opened/Mode/InputPos/…`、`StateMachine.SearchKeyword/CmdInput`、`LineNumMode.Name()`。

## model.go → model_*.go 拆分映射

| 原职责 | 现文件 | 说明 |
| --- | --- | --- |
| `editorModel` 结构体、常量、构造、`Init` | `core/model_types.go` | 含 rune/byte 列换算、`SwapTickMsg` |
| `Update` 消息分发 | `core/model_update.go` | |
| Edit 模式按键 + 光标移动/删除/滚动 | `core/model_edit.go` | |
| Preview 模式按键 + `renderPreview` | `core/model_preview.go` | |
| Command 模式 + `executeCommand` | `core/model_command.go` | 含 `expandPercent`/`loadFile` |
| 文件浏览器按键 | `core/model_filebrowser.go` | |
| `View`/`fallbackView`/`renderEdit` | `core/model_view.go` | 定义 `*EditorModel` 方法，必须与模型同包 |
| `rebuildPreview` | `core/model_rebuild.go` | block-aware 渲染缓存构建 |
| 渲染工具（行号/代码块/chroma） | `core/model_render_util.go` | |
| 工具函数（`touch`/`clamp`/`max`/`min`） | `core/model_util.go` | |

## 注意事项

- `core/model_view.go` 也是 `model` 的一部分（`View`/`renderEdit` 为 `*EditorModel` 方法），必须与 `EditorModel` 同包，因此 `core/` 实际含 **10** 个 `model_*.go`。
- 跨包调用均使用限定名：`core` 中为 `termd.Buffer`、`termd.WrapText` 等；`main.go` 中为 `core.NewModel`、`core.LoadTermdrc`、`termd.InitI18N` 等。
- 若需调整包边界（例如把更多文件移入/移出 `core/`），需同步处理未导出符号的导出改名，并以 `go build ./...` + `go vet ./...` 验证。
