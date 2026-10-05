# daw-session-model-go

一个音频工作站（DAW）会话与轨道模型库，用于编辑工程结构、维护轨道 / 片段 /
自动化曲线 / 效果链，并能把整个混音图离线渲染为音频。会话可随时保存、加载并
继续编辑，编辑历史支持撤销与重做。

包路径：`github.com/shikejing/daw-session-model-go/daw`

## 会话模型

```
Session
├── Version / Name / SampleRate
└── Tracks []*Track
    ├── ID / Name / GroupID          轨道标识与分组
    ├── Gain / Pan / Mute / Solo     推子、声像（-1..+1）、静音、独奏
    ├── Clips []Clip                 片段：Source、Start、Duration、Offset、Gain（采样单位）
    ├── Automation []AutomationCurve 自动化曲线：参数名 + 断点序列（线性插值）
    ├── Sends []Send                 发送：目标轨道 ID + 发送增益（推子后）
    └── Chain []PluginSlot           效果链：插件 ID、Enabled（false=旁路）、参数表
```

- 时间位置一律以**采样帧**为单位（int64），保证确定性。
- `Validate` 检查结构不变量：轨道 ID 唯一、发送目标存在、发送图无环路。
- `CheckAcyclic` 对发送图做拓扑环路检测；发现反馈环时返回 `*CycleError`，
  其 `TrackIDs` 按路径顺序列出涉事通道。合法的菱形路由等无环图不会被误判。

## 保存 / 加载与版本迁移

```go
daw.Save(w, session)          // 以当前格式版本（2）写出 JSON
res, err := daw.Load(r)       // 严格解码 + 自动迁移
res.Session                   // 加载后的会话
res.Migrations                // 迁移记录（From/To/Description），可解释
```

- **一致性**：保存前会对文档做规范化（空切片 / nil 的 canonical 形式），
  保存→加载→再保存得到的字节完全一致，会话字段不丢失、不被静默改写。
- **严格解码**：未知字段直接报错，而不是静默丢弃。
- **迁移**：旧版本（v1，无分组 / 发送 / 自动化 / 效果链字段）的工程加载时自动
  升级到当前版本，`LoadResult.Migrations` 逐步记录每次升级的来源、目标与说明；
  高于当前版本的文件会被拒绝。

## 撤销 / 重做

```go
h := daw.NewHistory(session)
h.Do(daw.Edit{Label: "add track", Apply: func(s *daw.Session) error { ... }})
h.Undo()  // 逆序回退
h.Redo()  // 重做
```

- 每次编辑记录前后快照；撤销 / 重做的结果与按顺序重放编辑命令完全一致。
- 撤销之后产生新编辑会丢弃整个重做栈（时间线分叉）。
- 应用失败的编辑不会进入历史，会话保持原状。
- `SetLimit(n)` 可限制撤销栈深度（0 = 不限）。

## 并发编辑

```go
ed := daw.NewEditor(session)
tx := ed.Begin()                       // 记录基准修订号
tx.Edit("add a", func(s *daw.Session) error { ... })
tx.Edit("add b", func(s *daw.Session) error { ... })
err := ed.Commit(tx)                   // 原子提交：整体生效或整体拒绝
```

- 事务在**会话副本**上应用，验证通过后才原地换入：不存在执行一半的编辑。
- 冲突通过 `*daw.Conflict`（可用 `errors.Is(err, daw.ErrConflict)` 判定、
  `errors.As` 取出）给出**可区分的原因**：
  - `ConflictRevision`：提交期间会话已被他人修改（乐观并发检查失败），
    调用方应重新 `Begin` 并重试；
  - `ConflictValidation`：编辑函数失败，或结果违反结构约束（环路、悬空发送、
    重复轨道 ID）。
- `Editor` 同时维护提交历史，`ed.Undo() / ed.Redo()` 可回退整个事务。

## 离线渲染

```go
r := daw.NewRenderer()
res, err := r.Render(ctx, session, daw.RenderOptions{
    Frames:      48000,            // 输出帧数（每声道）
    Seed:        42,               // 随机种子：相同输入+种子 => 逐样本一致
    Policy:      daw.FailFast,     // 插件故障策略
    Registry:    reg,              // nil 则使用内置注册表
    Incremental: true,             // 启用增量渲染缓存
})
res.Samples                       // 交错立体声，Frames*2 个 float64
res.Warnings                      // 非致命问题（如被旁路的故障插件）
```

### 渲染语义

1. 渲染前先做环路检测，含反馈环的会话直接拒绝并给出 `*CycleError`。
2. 轨道按发送图的拓扑序求值：发送方先于接收方。
3. 每条轨道：片段求和（片段音频由 Source ID 与种子确定性地合成）
   → 叠加入站发送 → 自动化（`gain` / `pan`，断点间线性插值）
   → 推子增益与等功率声像 → 依次经过效果链中**启用**的插件
   （`Enabled=false` 的槽位被旁路，不会调用）。
4. 静音轨道输出静默；任一轨道独奏时，只有独奏轨道及其发送下游可闻。
5. 所有轨道的推子后输出求和为混音总线。
6. 确定性：相同会话内容、选项与种子下，渲染结果逐样本一致。

### 插件故障策略（FailurePolicy）

- `FailFast`：渲染中止，返回 `*PluginError`，其中 `TrackID` / `Slot` /
  `PluginID` 精确定位故障通道。
- `BypassOnFailure`：故障插件按旁路处理，渲染继续并产生**全长**输出
  （不会静音或截断），同时在 `RenderResult.Warnings` 中记录故障位置。
  未注册的插件 ID 同样按插件故障处理。

### 插件

```go
type Plugin interface {
    Process(ctx, in []float64, sampleRate int, params map[string]float64, seed uint64) ([]float64, error)
}
```

内置插件：`gain`（线性增益）、`pan`（等功率声像）、`delay`（回声，
参数 `time` / `mix` / `feedback`）。自定义插件通过 `Registry.Register` 注册；
实现必须对相同输入、参数与种子保持确定性，且返回与输入等长的切片。

### 增量渲染

`Renderer` 内部按轨道内容哈希缓存「无入站发送」轨道的完整输出。重复渲染时，
只有内容发生变化的轨道会被重算，未改动的轨道直接命中缓存——渲染耗时与
**改动量**相关，而非工程总规模。`Renderer.Stats()` 返回累计的
`TracksRendered` / `CacheHits`，可用于观测缓存效果；`ResetCache()` 清空缓存。
增量渲染的输出与全量渲染逐样本一致（有测试保证）。

## 测试

```sh
go test ./... -race
```

覆盖：保存 / 加载一致性与 v1→v2 迁移、撤销 / 重做与重放等价、并发编辑冲突
（修订号冲突、校验冲突、半提交回滚、多提交者压力）、环路检测（含自环、
菱形不误判）、插件故障两种策略、静音 / 独奏 / 发送路由、自动化、渲染确定性、
增量渲染的正确性与工作量缩放。
