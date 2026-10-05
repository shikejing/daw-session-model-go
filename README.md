# daw-session-model-go

一个音频工作站（DAW）会话与轨道模型库，用于编辑工程结构（轨道、分组、发送、
片段、效果链），维护可撤销的编辑历史，支持并发编辑，并能把整个混音图离线渲染
成音频。代码位于 `daw/` 包。

## 会话模型

```
Session
├── version / sample_rate
├── tracks[]
│   ├── id, name, group_id          # 轨道分组
│   ├── gain, pan, mute, solo       # 通道条状态
│   ├── clips[]                     # 片段：start/duration/offset/gain/source_id（秒）
│   ├── sends[]                     # 发送：target_track_id, gain, pre_fader
│   └── chain                       # 效果链
│       └── slots[]                 # 插件槽：plugin_id, enabled, bypassed, params
└── master                          # 主输出效果链
```

- **片段时间位置**：`Clip.Start` 是时间线上的起点（秒），`Clip.Offset` 是源素材内
  的偏移，`Clip.Duration` 是片段长度。
- **发送（Send）**：把一条轨道的信号分送到另一条轨道（总线）。`PreFader=true`
  在效果链之后、推子之前取信号；否则在增益/声像之后取信号。
- **效果链**：按槽位顺序处理。`Enabled=false` 或 `Bypassed=true` 的槽位被跳过。

## 持久化与迁移

```go
data, _ := daw.Save(session)          // 确定性 JSON（键排序、固定缩进）
res, _ := daw.Load(data)              // res.Session + res.Migrations
```

- 保存→加载是严格往返：未知字段会被拒绝（`DisallowUnknownFields`），字段不会
  丢失或被静默改写。
- 文档带 `version`（当前为 2）。加载旧版本时逐级迁移，每个迁移都在
  `LoadResult.Migrations` 中给出 `from/to/summary` 说明。当前迁移：
  - **v1 → v2**：补充 master 效果链、片段增益（默认 1.0）、轨道发送列表。
- 比当前更新的版本会被拒绝并说明原因。

## 撤销 / 重做

```go
h := daw.NewHistory()
h.Do(session, &daw.AddTrackCmd{Track: t})
h.Undo(session)
h.Redo(session)
```

- 每个 `Command` 都实现 `Apply`/`Revert`，撤销重做等价于按顺序重放命令。
- 撤销之后执行新的 `Do` 会丢弃重做栈。
- 应用失败的命令不会进入历史。
- 内置命令：`AddTrackCmd`、`RemoveTrackCmd`、`SetTrackParamCmd`（gain/pan）、
  `AddClipCmd`、`AddSendCmd`（自带环路防护，会产生环路的发送被拒绝并回滚）。

## 并发编辑

```go
store := daw.NewStore(session)
tx := store.Begin()
tx.Do(&daw.SetTrackParamCmd{TrackID: "a", Field: "gain", Value: 0.5})
err := tx.Commit() // 原子生效，或整体被拒绝并返回 *ConflictError
```

乐观事务：每个事务编辑私有副本，提交时整体生效或整体拒绝，不存在执行一半的
编辑。冲突原因可区分（`ConflictError.Reason`）：

| 原因 | 含义 |
|---|---|
| `version_mismatch` | 基线版本已过期，其他事务先提交了 |
| `track_missing` | 并发提交删除了本事务编辑的轨道（`TrackID` 指明） |
| `routing_cycle` | 提交会引入发送环路（`TrackID` 指明涉事通道） |

## 离线渲染

```go
r := &daw.Renderer{Registry: daw.NewRegistry()} // 可选 Sources 提供真实音频
res, err := r.Render(session, daw.RenderConfig{
    Duration: 30, Policy: daw.FailFast, Seed: 42,
})
// res.Left / res.Right：float32 立体声；res.Failures：非致命插件故障
```

渲染语义：

1. 发送图做拓扑排序（Kahn 算法，字典序决胜，结果确定）；有环路则返回
   `*CycleError` 并列出涉事轨道。
2. 每条轨道：片段音频（`SourceProvider`，缺省时用由 `(sourceID, seed)` 决定的
   合成信号）+ 汇入的发送 → 效果链 → 增益/声像（等功率声像律）。
3. 静音/独奏只影响进入主混的部分：有独奏轨道时仅独奏轨道可闻，静音轨道永远
   不可闻。
4. 主混最后过 master 效果链。
5. **确定性**：相同会话、配置与随机种子下逐样本一致（含 `builtin.noise` 这类
   随机插件，其种子由 `Seed ^ hash(track) ^ hash(slot)` 派生）。

### 插件故障策略（`RenderConfig.Policy`）

- `FailFast`：渲染中止，返回 `*PluginError`，含 `TrackID`/`SlotID`/`PluginID`，
  可定位到具体通道。
- `BypassOnFailure`：故障插件被旁路（音频原样通过，绝不产生静音或残缺输出），
  故障记录进 `RenderResult.Failures`。

未知插件 ID 同样按上述策略处理。内置插件：`builtin.gain`（参数 `gain`）、
`builtin.noise`（参数 `amount`，确定性白噪声）。自定义插件用
`Registry.Register(id, factory)` 注册。

### 环路检测

`daw.ValidateGraph(session)` 独立可用。自发送、双向发送、多节点环都会被拒绝
并列出涉事轨道；菱形等合法 DAG 不会误判。渲染器与 `Store.Commit` 内部都会
执行该检查。

## 增量渲染

```go
ir := daw.NewIncrementalRenderer(renderer)
res, _ := ir.Render(session, cfg)   // 首次全量
// ... 修改少量片段 ...
res, _ = ir.Render(session, cfg)    // 只重算受影响的轨道
stats := ir.LastStats()             // TracksReused / TracksRecomputed
```

每条轨道的缓存键覆盖其自身内容（JSON 哈希）以及它接收发送的所有上游轨道的
键，因此一次编辑只使"改动轨道的下游锥"失效：重算量与改动量相关，与工程总
规模无关。缓存结果与全量渲染逐样本一致。

## 测试

```sh
go test ./...        # 全部测试
go test -race ./...  # 含并发竞争检测
```

覆盖：保存/加载一致性与迁移、撤销/重做与重放等价、并发编辑冲突（版本冲突、
轨道删除、环路）、渲染确定性、增益/声像/静音/独奏/发送、插件故障两种策略、
环路检测与误判、增量渲染的失效范围与一致性。
