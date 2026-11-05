# 运行示例

在 `nmatcher` 目录执行：

```sh
go run ./examples
```

程序先演示查询和取消，再用一张双人票、八张单人票组成一局 5v5。整局 MMR 跨度先限制为 300，约 1 秒后放宽为 500 才能容纳这 10
人。成功后输出双方玩家、玩家 MMR 和阵营平均 MMR，并自动退出。玩家 1、2 属于同一张票，默认会留在同一阵营。

## 最少需要配置什么

- `WithTeamInfo`：阵营及容量。
- `WithOnMatched`：成功提交并移除票后，通知业务创建房间。
- `WithOnTimeout`：超时票被移除后，通知业务。
- 入队参数中的 `Index`、`Players` 和包含阶段 `0` 的 `Strategies`。

`MMR` 是业务提供的玩家实力分。策略中的 `MMR.MaxSpread` 控制选人范围，`WithMMRBalance(true)` 平衡选定玩家的阵营平均实力，二者可独立使用。赛后
MMR 仍由业务更新。`ExtraInfo` 用于携带昵称等业务信息，不会被 MMR 算法读取。

## 按 MMR 选人和等待放宽

```go
strategies := map[int]nmatcher.MatchStrategy{
    0:  {MMR: &nmatcher.MMRCondition{MaxSpread: 300}},
    10: {MMR: &nmatcher.MMRCondition{MaxSpread: 500}},
}
```

- `MaxSpread` 限制整局玩家的最高与最低 MMR 差，包含同票内的玩家；各张票当前的限制都必须满足。
- `MMR: nil` 表示不限制；`MaxSpread: 0` 要求玩家 MMR 完全相同。
- 启用 MMR 条件时，索引先找票平均 MMR 接近的候选，再检查属性条件、双方限制和整局跨度。同距离再比较加分、等待时间和 Id。
- MMR 条件与 `Required` 同时生效；一个 OR 属性组命中不能绕过 MMR 条件。
- `Ticket.MMRMin`、`MMRMax`、`MMRAverage` 由入队时计算，不需要填写，也不占用业务 `NumberData`。
- 每个阶段完整替换前一阶段；新阶段未填写 `MMR`，表示移除旧限制。

## 入队、查询与取消

```go
ticketId, err := matcher.Add(param)
// 先处理 err；入队失败时 ticketId 为空，票和索引不会写入。

ticket, exists := matcher.Get(ticketId)
ticket, exists = matcher.GetByIndex(param.Index)

err = matcher.CancelTicket(ticketId) // 精确取消本次入队。
err = matcher.Cancel(param.Index)    // 取消这个业务标识当前对应的票；与上一行二选一。
```

`Index` 在排队期间必须唯一，同一玩家不能同时出现在两张票中。相同 `Index` 取消后重新入队，会得到新的 `TicketId`；旧 Id
不会取消新票。请处理每次调用的错误，不要丢弃 `Add` 返回的错误。

查询和回调返回独立副本。查询结果只代表读取时的状态，此后票可能被并发取消或匹配。匹配、取消或超时移除后，查询返回 `nil, false`
，不保留终态历史。

## 自定义条件与等待后放宽

`Ticket.StringData`、`Ticket.NumberData` 是票提供的检索属性，`Strategies` 描述这张票接受什么样的其他票。双方都接受彼此才能匹配。

下面先要求同地区且等级在 25～35，等待 50 轮后取消等级限制：

```go
param.StringData = map[string]string{"region": "asia"}
param.NumberData = map[string]float64{"level": 30}
param.Strategies = map[int]nmatcher.MatchStrategy{
    0: {
        Required: []nmatcher.MatchConditions{{
            StringEquals: map[string]string{"region": "asia"},
            NumberRanges: map[string]nmatcher.NumberRange{
                "level": {Min: 25, Max: 35},
            },
        }},
    },
    50: {
        Required: []nmatcher.MatchConditions{{
            StringEquals: map[string]string{"region": "asia"},
        }},
    },
}
```

每个阶段完整替换上一个阶段的策略，不是追加条件。等待轮数按 `等待时长 / MatchInterval` 计算；本例为 100ms 一轮，50 轮对应约 5
秒。

- `Required` 的多个条件组是 OR，同一组内是 AND；为空表示不限制。
- 配置了条件的属性必须存在，包含 `StringNotEquals`。
- `Bonuses` 只影响通过必要条件后的候选优先级，不代替必要条件。
- `Timeout` 为 0 表示不超时，正数表示最长排队时间。
- `Pass` 可选，用于检查完整对局；可能被调用多次，通过也不代表已经提交成功。业务开房放在 `OnMatched` 中。

## 更换游戏规模

| 场景             | `TeamInfo`                      |
|------------------|---------------------------------|
| 5v5              | 两个阵营，各 5 人               |
| 百人局，4 人一队 | 25 个阵营，各 4 人              |
| 不同人数的多阵营 | 例如 `{"a": 4, "b": 5, "c": 6}` |

默认整张票进入同一阵营，票人数不能超过最大阵营容量。通过 `WithAllowSplitTicket(true)` 允许拆票后，票人数不能超过整局总容量。

`Start(ctx)` 在当前 goroutine 中持续运行，退出时取消 ctx 并等待它返回。也可以不启动 ticker，由业务自行调用 `RunOnce()`
；两种调度方式选择一种即可。
