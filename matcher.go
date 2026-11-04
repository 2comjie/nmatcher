package nmatcher

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2comjie/ntool/logx/logdef"
)

type Matcher[T PlayerId] struct {
	logdef.ILogger
	options *options[T]

	frameIndex atomic.Int64

	matchIndex MatchIndex[T]

	// 同一个match的匹配计算 不阻塞 Add Cancel
	matchMu sync.Mutex
}

// NewMatcher 默认每 100ms 调度一次；配置在构造时确定，运行期间保持不变。
func NewMatcher[T PlayerId](logger logdef.ILogger, opts ...Option[T]) *Matcher[T] {
	if logger == nil {
		panic("nmatcher: logger is required")
	}
	o := &options[T]{MatchInterval: 100 * time.Millisecond}
	for _, option := range opts {
		option(o)
	}
	if len(o.TeamInfo) == 0 {
		panic("nmatcher: TeamInfo is required")
	}
	if o.OnMatched == nil || o.OnTimeout == nil {
		panic("nmatcher: OnMatched and OnTimeout are required")
	}
	// 零值表示使用默认配置，在构造时统一确定，匹配期间直接使用。
	if o.MaxSeedsPerTick == 0 {
		o.MaxSeedsPerTick = 1000
	}
	if o.MaxSearchStepsPerTick == 0 {
		o.MaxSearchStepsPerTick = 10000
	}
	if o.MaxActiveAttempts == 0 {
		o.MaxActiveAttempts = 3
	}
	if o.ActiveCooldown == 0 {
		o.ActiveCooldown = 10 * o.MatchInterval
	}
	if o.MaxScanned == 0 {
		totalPlayers := 0
		for _, count := range o.TeamInfo {
			totalPlayers += count
		}
		o.MaxScanned = max(100, totalPlayers*10)
	}
	return &Matcher[T]{ILogger: logger, options: o}
}

func (m *Matcher[T]) Start(ctx context.Context) {
	ticker := time.NewTicker(m.options.MatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RunOnce()
		}
	}
}

func (m *Matcher[T]) RunOnce() {
	logCtx := m.WithField("action", "RunOnce").WithField("frameIndex", m.frameIndex.Load())
	defer func() {
		m.frameIndex.Add(1)
	}()

	logCtx.Info("开始一轮匹配")

	results, timeoutTickets := m.MakeMatch(time.Now())

	for _, result := range results {
		m.options.OnMatched(result.Clone())
	}

	for _, ticket := range timeoutTickets {
		m.options.OnTimeout(ticket.Clone())
	}

	logCtx.Info("一轮匹配完成")
}

// MakeMatch 执行一轮匹配，返回已提交的对局和本轮成功移除的超时票；通知由 RunOnce 调用。
// 流程：建立视图 -> 清理超时票 -> 领取到期 seed -> 搜索和评分 -> 回溯组局、分阵营 -> 原子提交。
// 在预算内寻找第一个通过校验的组合，不保证整局总得分最优，也不保证遍历完全部排队票。
func (m *Matcher[T]) MakeMatch(now time.Time) ([]*MatchResult[T], []*Ticket[T]) {
	// 只串行化 MakeMatch；Add、Cancel 使用索引自身的锁，可以在匹配计算期间执行。
	m.matchMu.Lock()
	defer m.matchMu.Unlock()
	logCtx := m.WithField("action", "MakeMatch").WithField("frameIndex", m.frameIndex.Load())
	logCtx.Infof("开始MakeMatch")
	// 三类预算分别限制主动票数、每张主动票的候选扫描数、整轮回溯节点数。
	// 回溯预算不包含超时扫描、候选校验和排序，因此不等同于整轮耗时上限。
	maxSeeds, maxSearchSteps := m.options.MaxSeedsPerTick, m.options.MaxSearchStepsPerTick
	maxActiveAttempts := m.options.MaxActiveAttempts
	cooldown := m.options.ActiveCooldown

	type teamCapacity struct {
		Team  Team
		Count int
	}
	type scoredTicket struct {
		Ticket *Ticket[T]
		Score  float64
	}

	teams := make([]teamCapacity, 0, len(m.options.TeamInfo))
	totalPlayers := 0

	// 汇总各阵营容量；固定阵营顺序，避免 map 遍历顺序改变分配次序。
	for team, count := range m.options.TeamInfo {
		teams = append(teams, teamCapacity{Team: team, Count: count})
		totalPlayers += count
	}
	maxScanned := m.options.MaxScanned
	slices.SortFunc(teams, func(a, b teamCapacity) int {
		return cmp.Compare(a.Team, b.Team)
	})

	// 整轮独占一个 COW 快照，搜索、评分和回溯均不持有索引锁。
	// 提交成功后裁剪此快照的候选索引，其他调用者持有的只读视图保持不变。
	// 此后并发新增的票下一轮可见；取消或超时造成的失效在提交时再次检查。
	view := m.matchIndex.View()

	var timeoutTickets []*Ticket[T]

	// 当前超时清理仍遍历视图内全部票，不受 seed 和候选扫描预算限制。
	view.tickets.Scan(func(id string, ticket *Ticket[T]) bool {
		if !ticket.ExpireTime.IsZero() && !now.Before(ticket.ExpireTime) {
			// 短写事务重新核实实时状态；只有实际移除成功才产生一次超时通知。
			if m.matchIndex.ExpireTicket(id) {
				cloned := ticket.Clone()
				timeoutTickets = append(timeoutTickets, &cloned)
			}
		}
		return true
	})

	// 仅到期票主动发起搜索；冷却中的票仍可在后续 Search 中被选作候选。
	seeds := view.Active(now, maxSeeds)
	if len(seeds) == 0 {
		return nil, timeoutTickets
	}

	// selected 是已选票，placements[i] 是第 i 张票所分配的阵营下标。
	// 结果深拷贝票数据，TeamMap 中的玩家引用结果自身 TicketMap 中的玩家。
	buildResult := func(selected []*Ticket[T], placements []int) *MatchResult[T] {
		result := &MatchResult[T]{
			Id:        rand.Text(),
			TeamMap:   make(map[Team][]*Player[T], len(teams)),
			TicketMap: make(map[string]*Ticket[T], len(selected)),
		}

		for _, team := range teams {
			result.TeamMap[team.Team] = make([]*Player[T], 0, team.Count)
		}

		splitTeam := 0
		for i, ticket := range selected {
			cloned := ticket.Clone()
			result.TicketMap[ticket.Id] = &cloned

			uids := make([]T, 0, len(cloned.Players))
			for uid := range cloned.Players {
				uids = append(uids, uid)
			}
			slices.Sort(uids)

			for _, uid := range uids {
				team := placements[i]

				if m.options.AllowSplitTicket {
					// 允许拆票时，按固定阵营和玩家顺序依次填满容量，不做阵营间实力平衡。
					for len(result.TeamMap[teams[splitTeam].Team]) ==
						teams[splitTeam].Count {
						splitTeam++
					}
					team = splitTeam
				}

				name := teams[team].Team
				result.TeamMap[name] = append(
					result.TeamMap[name],
					cloned.Players[uid],
				)
			}
		}

		return result
	}

	var results []*MatchResult[T]

	// 候选 Id 和评分缓冲区只在本轮内复用，避免每张 seed 重复分配。
	var candidateIds []string
	var ranked []scoredTicket
	// seeds 已在裁剪前生成，用集合跳过已提交 seed，避免额外查询票树。
	consumed := make(map[string]bool)
	// 所有 seed 共用回溯预算，防止每张票分别耗尽一次大规模组合搜索。
	steps := maxSearchSteps
	for _, id := range seeds {
		if steps <= 0 {
			break
		}
		if consumed[id] {
			continue
		}
		// 短写事务确认 seed 仍存在且到期，更新尝试次数并安排下一次主动搜索。
		seed, exists := m.matchIndex.beginAttempt(id, now, m.options.MatchInterval, cooldown, maxActiveAttempts)
		if !exists {
			continue
		}
		if len(seed.Players) == 0 || len(seed.Players) > totalPlayers {
			continue
		}
		strategy := seed.StrategyAt(int(now.Sub(seed.CreateTime) / m.options.MatchInterval))
		ranked = ranked[:0]
		// 索引只做必要条件粗筛；先检查完整双向接受，再累加双方对彼此的加分。
		candidateIds = view.search(strategy, maxScanned, candidateIds)
		for _, candidateId := range candidateIds {
			if candidateId == seed.Id {
				continue
			}
			ticket, exists := view.tickets.Get(candidateId)
			if !exists || len(ticket.Players) == 0 || (!ticket.ExpireTime.IsZero() && !now.Before(ticket.ExpireTime)) {
				continue
			}
			otherStrategy := ticket.StrategyAt(int(now.Sub(ticket.CreateTime) / m.options.MatchInterval))
			if !strategy.Accept(ticket) || !otherStrategy.Accept(seed) {
				continue
			}
			ranked = append(ranked, scoredTicket{Ticket: ticket, Score: strategy.bonusScore(ticket) + otherStrategy.bonusScore(seed)})
		}
		// 优先尝试与 seed 互评分高的票；同分时优先排队更久的票，再按 Id 排序。
		slices.SortFunc(ranked, func(a, b scoredTicket) int {
			if order := cmp.Compare(b.Score, a.Score); order != 0 {
				return order
			}
			if order := a.Ticket.CreateTime.Compare(b.Ticket.CreateTime); order != 0 {
				return order
			}
			return cmp.Compare(a.Ticket.Id, b.Ticket.Id)
		})
		// suffix[pos] 是剩余候选总人数；即使全选也凑不齐时，直接剪枝。
		suffix := make([]int, len(ranked)+1)
		for i := len(ranked) - 1; i >= 0; i-- {
			suffix[i] = suffix[i+1] + len(ranked[i].Ticket.Players)
		}
		remaining := make([]int, len(teams))
		for i, team := range teams {
			remaining[i] = team.Count
		}
		if m.options.AllowSplitTicket {
			// 允许拆票时，选票阶段只检查总容量，具体玩家分配交给 buildResult。
			remaining = []int{totalPlayers}
		}
		selected := []*Ticket[T]{seed}
		placements := []int{0}
		var matched *MatchResult[T]
		var search func(int, int) bool
		// 深度优先回溯：pos 是当前候选下标，needed 是距离满局还缺的人数。
		// 每个节点先尝试选入当前票并分配阵营，失败后跳过当前票继续搜索。
		search = func(pos, needed int) bool {
			if steps <= 0 {
				return false
			}
			steps--
			if needed == 0 {
				result := buildResult(selected, placements)
				// Pass 在索引锁外接收独立副本，拒绝后继续尝试其他组合。
				// 同一轮可能检查多个组合，Pass 通过也不代表最终一定提交成功。
				if m.options.Pass != nil && !m.options.Pass(result.Clone()) {
					return false
				}
				// 短写事务按原 Ticket.Id 验证所有票仍在队且未过期，再一次性删除。
				// 与 Cancel 竞争时先提交者生效；同 Index 重入的新票无法替代旧票。
				if !m.matchIndex.CommitMatch(result) {
					return false
				}
				matched = result
				return true
			}
			if pos == len(ranked) || suffix[pos] < needed {
				return false
			}
			ticket := ranked[pos].Ticket
			compatible := len(ticket.Players) <= needed
			if compatible {
				// 与 seed 接受彼此还不够，整局所有票必须两两满足双方的必要条件。
				for _, other := range selected {
					if !m.CanMatch(ticket, other, now) {
						compatible = false
						break
					}
				}
			}
			if compatible {
				for team, capacity := range remaining {
					if capacity < len(ticket.Players) {
						continue
					}
					// 不允许拆票时，整张票必须放进同一阵营；记录选择后递归。
					remaining[team] -= len(ticket.Players)
					selected = append(selected, ticket)
					placements = append(placements, team)
					if search(pos+1, needed-len(ticket.Players)) {
						return true
					}
					// 此分支失败，撤销选票、阵营分配和容量扣减，再尝试其他阵营。
					selected = selected[:len(selected)-1]
					placements = placements[:len(placements)-1]
					remaining[team] += len(ticket.Players)
					if steps <= 0 {
						return false
					}
				}
			}
			// 当前票未选入，人数需求不变，尝试下一张候选。
			return search(pos+1, needed)
		}
		// seed 必须参与本局，依次尝试它能容纳的阵营，再递归补齐其他票。
		for team, capacity := range remaining {
			if capacity < len(seed.Players) {
				continue
			}
			remaining[team] -= len(seed.Players)
			placements[0] = team
			if search(0, totalPlayers-len(seed.Players)) {
				break
			}
			remaining[team] += len(seed.Players)
			if steps <= 0 {
				break
			}
		}
		if matched != nil {
			results = append(results, matched)
			for ticketId := range matched.TicketMap {
				// 后续搜索直接看到剩余候选，已提交票不再消耗扫描预算。
				view.removeMatched(ticketId)
				consumed[ticketId] = true
			}
		}
	}
	return results, timeoutTickets
}

// CanMatch 判断双方当前策略是否互相接受，不检查阵营容量和分队约束
func (m *Matcher[T]) CanMatch(a, b *Ticket[T], now time.Time) bool {
	aRounds := int(now.Sub(a.CreateTime) / m.options.MatchInterval)
	bRounds := int(now.Sub(b.CreateTime) / m.options.MatchInterval)
	aStrategy := a.StrategyAt(aRounds)
	bStrategy := b.StrategyAt(bRounds)
	return aStrategy.Accept(b) && bStrategy.Accept(a)
}

type AddParam[T PlayerId] struct {
	Index      string
	Players    map[T]AddPlayer[T]
	StringData map[string]string
	NumberData map[string]float64
	Strategies map[int]MatchStrategy
	Timeout    time.Duration
}

type AddPlayer[T PlayerId] struct {
	Uid       T
	ExtraInfo map[string]string
}

func (m *Matcher[T]) Add(param AddParam[T]) error {
	logCtx := m.WithField("action", "Add")
	if len(param.Players) == 0 || param.Index == "" || param.Timeout < 0 {
		return errors.New("invalid param")
	}
	if _, exists := param.Strategies[0]; !exists {
		return errors.New("invalid param")
	}
	for _, strategy := range param.Strategies {
		for _, bonus := range strategy.Bonuses {
			if bonus.Score < 0 || math.IsNaN(bonus.Score) || math.IsInf(bonus.Score, 0) {
				return errors.New("bonus score must be finite and non-negative")
			}
		}
	}

	ticket := Ticket[T]{
		Id:         rand.Text(),
		Index:      param.Index,
		StringData: param.StringData,
		NumberData: param.NumberData,
		Strategies: param.Strategies,
	}

	logCtx = logCtx.WithField("id", ticket.Id).WithField("index", ticket.Index)
	ticket.Players = make(map[T]*Player[T], len(param.Players))

	for uid, player := range param.Players {
		ticket.Players[uid] = &Player[T]{
			Uid:       uid,
			ExtraInfo: player.ExtraInfo,
			TicketId:  ticket.Id,
		}
	}
	ticket.CreateTime = time.Now()
	if param.Timeout > 0 {
		ticket.ExpireTime = ticket.CreateTime.Add(param.Timeout)
	}

	if err := m.matchIndex.Add(&ticket); err != nil {
		logCtx.Warnf("添加票索引失败  %v", err)
		return err
	}
	logCtx.Info("匹配票入队成功")
	return nil
}

func (m *Matcher[T]) Cancel(index string) error {
	logCtx := m.WithField("action", "Cancel").WithField("index", index)
	if index == "" {
		return errors.New("invalid param")
	}

	if err := m.matchIndex.Cancel(index); err != nil {
		return err
	}

	logCtx.Infof("取消匹配成功")
	return nil
}

// CancelTicket 精确取消某次入队，旧请求不会取消相同业务 Index 下的新票。
func (m *Matcher[T]) CancelTicket(ticketId string) error {
	if ticketId == "" {
		return errors.New("invalid param")
	}
	return m.matchIndex.CancelTicket(ticketId)
}

func (m *Matcher[T]) CommitMatch(result *MatchResult[T]) bool {
	return m.matchIndex.CommitMatch(result)
}
