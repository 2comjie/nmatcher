# nmatcher

Go 单进程内存匹配器，支持单人票、组队票、多阵营、自定义条件、等待放宽和 MMR 匹配。

业务提交玩家和匹配策略，匹配器负责选票、分配阵营、处理取消与超时，并通过回调通知业务。房间创建、网络连接以及赛后 MMR 更新由业务处理。

## 安装与运行

Go 版本要求见 [go.mod](go.mod)，当前为 Go 1.27.3。

```sh
go get github.com/2comjie/nmatcher
```

克隆仓库后，可以直接运行完整示例：

```sh
go run ./examples
```

[示例说明](examples/README.md) 包含查询、取消、双人组队、MMR 等待放宽和退出流程。

## 快速上手：组成一局 5v5

下面的程序提交 10 张单人票，执行一轮匹配，并输出双方人数和 MMR。

```go
package main

import (
	"fmt"
	"time"

	"github.com/2comjie/nmatcher"
	"github.com/2comjie/ntool/logx"
)

func main() {
	matcher := nmatcher.NewMatcher[uint64](logx.WithName("match-demo"),
		nmatcher.WithTeamInfo[uint64](map[nmatcher.Team]int{"red": 5, "blue": 5}),
		nmatcher.WithMMRBalance[uint64](true),
		nmatcher.WithOnMatched[uint64](func(result nmatcher.MatchResult[uint64]) {
			fmt.Printf("匹配成功：%s\n", result.Id)
			for _, team := range []nmatcher.Team{"red", "blue"} {
				fmt.Printf("%s 阵营，共 %d 人：", team, len(result.TeamMap[team]))
				for _, player := range result.TeamMap[team] {
					fmt.Printf(" %d(MMR=%.0f)", player.Uid, player.MMR)
				}
				fmt.Println()
			}
		}),
		nmatcher.WithOnTimeout[uint64](func(ticket nmatcher.Ticket[uint64]) {
			fmt.Printf("匹配超时：%s\n", ticket.Index)
		}),
	)

	for uid := uint64(1); uid <= 10; uid++ {
		_, err := matcher.Add(nmatcher.AddParam[uint64]{
			Index:   fmt.Sprintf("player-%d", uid),
			Players: map[uint64]nmatcher.AddPlayer[uint64]{uid: {MMR: 1500 + float64(uid)*10}},
			Strategies: map[int]nmatcher.MatchStrategy{
				0: {MMR: &nmatcher.MMRCondition{MaxSpread: 200}},
			},
			Timeout: 30 * time.Second,
		})
		if err != nil {
			panic(err)
		}
	}

	matcher.RunOnce()
}
```

`RunOnce()` 执行匹配、提交结果并调用成功与超时回调。持续服务通常使用 `Start(ctx)`：

```go
ctx, cancel := context.WithCancel(context.Background())
stopped := make(chan struct{})
go func() {
	defer close(stopped)
	matcher.Start(ctx)
}()

// 运行期间可并发调用 Add、Get、GetByIndex、Cancel、CancelTicket。

// 服务退出时：
cancel()
<-stopped
```

`Start` 默认每 100ms 执行一轮，在调用它的 goroutine 中阻塞运行。也可以由业务自行调度 `RunOnce()`，两种方式选择一种即可。

## 票、玩家和阵营

| 概念 | 用途 |
| --- | --- |
| `Player` | 玩家 Id、MMR 和业务附加信息 |
| `Ticket` | 一次入队，包含一个或多个玩家及其匹配策略 |
| `Ticket.Index` | 业务唯一标识，例如队伍 Id 或带类型前缀的玩家 Id |
| `Ticket.Id` | 匹配器生成的本次入队 Id，取消后重新入队会改变 |
| `Team` | 对局中的阵营或队伍名称 |
| `MatchResult.TeamMap` | 各阵营最终分配的玩家 |
| `MatchResult.TicketMap` | 组成这局的全部票 |

玩家 Id 支持字符串、整数，以及对应的自定义底层类型。`Players` 的 map key 是权威玩家 Id，不需要重复填写 `AddPlayer.Uid`。

同一玩家不能同时出现在两张排队票中，`Index` 在排队期间也必须唯一。多人组队时，将所有玩家放进同一张票即可：

```go
param := nmatcher.AddParam[uint64]{
	Index: "party-42",
	Players: map[uint64]nmatcher.AddPlayer[uint64]{
		101: {MMR: 1800, ExtraInfo: map[string]string{"nickname": "小明"}},
		102: {MMR: 1700, ExtraInfo: map[string]string{"nickname": "小红"}},
	},
	Strategies: map[int]nmatcher.MatchStrategy{0: {}},
	Timeout:    30 * time.Second,
}
```

默认不拆票，同一张票的玩家进入同一阵营。`WithAllowSplitTicket(true)` 允许将他们分到不同阵营。`ExtraInfo` 用于携带业务数据，MMR 算法不读取它。

`TeamInfo` 可以配置两队、多队或人数不相同的阵营。例如 5v5 使用两个容量为 5 的阵营，百人四人小队模式使用 25 个容量为 4 的阵营。

## 查询和取消

```go
ticketId, err := matcher.Add(param)
if err != nil {
	// 处理重复玩家、重复 Index 或非法参数等入队错误。
	return
}

ticket, exists := matcher.Get(ticketId)
ticket, exists = matcher.GetByIndex(param.Index)

if err := matcher.CancelTicket(ticketId); err != nil {
	// 已出队时返回 nmatcher.ErrTicketNotFound。
	return
}
```

也可以使用 `matcher.Cancel(param.Index)` 取消当前业务票。精确取消某次入队时使用 `CancelTicket`，这样旧请求不会误取消同一 `Index` 下重新入队的新票。

查询返回深拷贝，只代表读取时的状态。匹配、取消或超时移除后，查询返回 `nil, false`。`ErrTicketNotFound` 只表示票已经不在队列，匹配器不保存终态历史。

## 自定义条件与加分

`StringData`、`NumberData` 是票提供的属性；`Strategies` 描述这张票接受什么样的其他票。属性条件对票生效，不会自动从玩家的 `ExtraInfo` 中读取。

```go
param.StringData = map[string]string{"region": "asia", "mode": "ranked"}
param.NumberData = map[string]float64{"level": 30}
param.Strategies = map[int]nmatcher.MatchStrategy{
	0: {
		Required: []nmatcher.MatchConditions{{
			StringEquals: map[string]string{"region": "asia"},
			NumberRanges: map[string]nmatcher.NumberRange{
				"level": {Min: 25, Max: 35},
			},
		}},
		Bonuses: []nmatcher.MatchBonus{{
			Score: 10,
			AnyOf: []nmatcher.MatchConditions{{
				StringEquals: map[string]string{"mode": "ranked"},
			}},
		}},
	},
}
```

- `Required` 中的条件组为 OR，同一个组中的字段为 AND；空 `Required` 表示不限制属性。
- `StringEquals` 表示等于，`StringNotEquals` 表示不等于，`NumberRanges` 包含两端点。
- 配置的属性必须存在，包括不等条件；缺失时该组不通过。
- 双方必须互相接受，组合内所有票也必须两两满足属性条件。
- `Bonuses` 不参与必要条件筛选。一条 bonus 的任意组命中后加一次分，多条 bonus 可以累加；空 `AnyOf` 表示无条件加分。
- 候选优先级使用双方加分之和，同分时优先排队更久的票，再按票 Id 排序。

## MMR 选人、等待放宽和分队

玩家通过 `AddPlayer.MMR` 提供实力分。入队时自动生成 `Ticket.MMRMin`、`MMRMax`、`MMRAverage` 并建立独立索引，不占用业务 `NumberData`。

```go
param.Strategies = map[int]nmatcher.MatchStrategy{
	0:  {MMR: &nmatcher.MMRCondition{MaxSpread: 100}},
	50: {MMR: &nmatcher.MMRCondition{MaxSpread: 300}},
	100: {}, // 不再限制 MMR。
}
```

`MaxSpread` 限制整局所有玩家的最高与最低 MMR 差，包含同票内玩家。每张票当前的限制都必须满足：如果 A 接受 100、B 接受 300，组成的对局仍需满足 A 的 100。

- `MMR: nil`：不限制 MMR；`MaxSpread: 0`：要求玩家 MMR 完全相同。
- 启用 MMR 条件后，优先从票平均 MMR 接近的位置搜索；通过完整条件后，先比较 MMR 距离，再比较加分、等待时间和 Id。
- MMR 条件与属性 `Required` 同时生效，OR 属性组命中不能绕过 MMR 限制。
- 等待轮数为 `等待时长 / MatchInterval`，与主动尝试次数不同。默认 100ms 一轮，50 轮约为 5 秒。
- 每个阶段完整替换前一阶段，不继承遗漏的字段。需要保留的属性条件、加分规则和 MMR 限制应在新阶段重新填写。

`WithMMRBalance(true)` 独立开启阵营实力平衡。选定玩家后，算法在预算内减小各阵营平均 MMR 的方差：小规模可枚举时搜索合法分配，大规模通过等人数单元交换改善。不拆票时整张票作为一个单元。预算耗尽后保留合法分队，不保证全局最优。

MMR 的计算、赛后涨跌和持久化由业务负责。

## 回调和并发语义

- `OnMatched`：整局票已经原子移出队列后调用，在这里通知玩家或创建房间。
- `OnTimeout`：超时票成功移除后调用；`Timeout: 0` 表示不超时。
- `Pass`：可选的完整结果检查。返回 `false` 继续搜索其他组合；可能被多次调用，通过也不代表最终提交一定成功。它应只负责检查，不在这里开房。
- 回调和查询均获得深拷贝，修改副本不会改变内部票或索引。
- 取消与提交竞争时，先成功移除票的一方生效。提交失败不会部分删除其余票；旧 `TicketId` 不能替代同 `Index` 下的新票。
- `Add` 复制输入数据，调用完成后可复用参数；调用过程中不要并发修改同一份输入 map。

匹配计算在 COW 快照上进行，搜索、组局和 `Pass` 不持有实时索引锁；入队、取消和提交使用短写事务。同一 `Matcher` 的匹配计算串行执行。

`Start` 中的回调同步执行，慢回调会延长实际匹配周期。低层接口 `MakeMatch(now)` 返回已经提交的对局和已移除的超时票，但不执行通知；通常直接使用 `RunOnce` 或 `Start` 即可。

## 配置速查

| Option | 默认值 | 含义 |
| --- | --- | --- |
| `WithTeamInfo` | 必填 | 阵营名称和容量 |
| `WithOnMatched` | 必填 | 匹配成功回调 |
| `WithOnTimeout` | 必填 | 超时回调 |
| `WithPass` | 无 | 自定义完整结果检查 |
| `WithMatchInterval` | `100ms` | 定时匹配间隔 |
| `WithAllowSplitTicket` | `false` | 允许拆开同一张票的玩家 |
| `WithMMRBalance` | `false` | 启用阵营平均 MMR 平衡 |
| `WithMaxScanned` | `max(100, 整局人数*10)` | 每张 seed 的候选扫描预算 |
| `WithMaxSeedsPerTick` | `1000` | 每轮最多主动尝试的到期票数 |
| `WithMaxSearchStepsPerTick` | `10000` | 每轮共享的组局回溯节点预算 |
| `WithMaxMMRBalanceStepsPerTick` | `100000` | 每轮共享的 MMR 平衡搜索预算 |
| `WithMaxActiveAttempts` | `3` | 一个重试批次内连续主动搜索次数 |
| `WithActiveCooldown` | `10 * MatchInterval` | 达到次数上限后暂停主动搜索的时间 |

预算和冷却时间设置为 0 时使用默认值。冷却中的票仍可被其他 seed 选中，策略阶段改变时会提前唤醒主动搜索。

## 校验和使用边界

构造时缺少必需配置会 panic。`Add` 遇到非法参数返回错误和空票 Id，包括：空玩家、空 `Index`、负超时、缺少阶段 0、负策略轮数、倒置区间、非有限玩家 MMR、非法 MMR 跨度，以及非有限或负的 bonus 分数。

不拆票时，票人数不能超过最大阵营容量；允许拆票时不能超过整局总容量。

当前匹配器运行在单进程内存中，不提供跨进程共享或重启恢复。选票、组局和平衡都有预算，不保证找到所有可行组合或全局最高分。候选扫描预算包含 seed、被拒绝的票和 OR 分支中的重复扫描。

回溯节点预算不等于耗时上限：超时清理仍全量扫描队列，候选校验、排序、结果复制和回调也需要时间。设置 20ms tick 不代表每轮一定能在 20ms 内完成，容量应使用实际机器、规则和玩家数据测量。

## 测试和性能测试

```sh
go test -race -timeout 60s ./...

# 十万张票、十人局的单轮匹配。
go test -run '^$' -bench '^BenchmarkMakeMatch/N=100000/players=10$' -benchmem -cpu=8

# 20ms tick，持续补票维持十万票，开启 MMR 选票与分队。
go test -run '^$' -bench '^BenchmarkMatchTick20ms/MMR-range-steps=1000$' -benchtime=200x -benchmem -cpu=8
```

基准输出包含匹配人数、轮次、耗时分位数、分配量和 GC 数据。持续补票基准的轮次耗时包含结果复制、回调和重新入队，不等同于单次 `MakeMatch` 耗时。

许可证见 [LICENSE](LICENSE)。
