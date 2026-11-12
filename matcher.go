package nmatcher

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2comjie/ntool/logx/logdef"
)

type Matcher[T PlayerId] struct {
	logger  logdef.ILogger
	options *options[T]

	frameIndex atomic.Int64

	matchIndex matchIndex[T]

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
	if o.MaxMMRBalanceStepsPerTick == 0 {
		o.MaxMMRBalanceStepsPerTick = 100000
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
	return &Matcher[T]{
		logger:  logger,
		options: o,
	}
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
	logCtx := m.logger.WithField("action", "RunOnce").WithField("frameIndex", m.frameIndex.Load())
	defer func() {
		m.frameIndex.Add(1)
	}()

	logCtx.Info("开始一轮匹配")

	results, timeoutTickets := m.makeMatch(time.Now())

	for _, result := range results {
		m.options.OnMatched(result.Clone())
	}

	for _, ticket := range timeoutTickets {
		m.options.OnTimeout(ticket.Clone())
	}

	logCtx.Info("一轮匹配完成")
}

// Add 入队成功后返回本次入队的 TicketId；失败时返回空字符串。
func (m *Matcher[T]) Add(param AddParam[T]) (string, error) {
	logCtx := m.logger.WithField("action", "Add")
	if len(param.Players) == 0 || param.Index == "" || param.Timeout < 0 {
		return "", errors.New("invalid param")
	}
	if _, exists := param.Strategies[0]; !exists {
		return "", errors.New("invalid param")
	}
	// 不拆票时必须有一个阵营能容纳整张票；允许拆票时只限制整局总人数。
	capacity := 0
	for _, count := range m.options.TeamInfo {
		if m.options.AllowSplitTicket {
			capacity += count
		} else {
			capacity = max(capacity, count)
		}
	}
	if len(param.Players) > capacity {
		return "", fmt.Errorf("ticket player count %d exceeds capacity %d", len(param.Players), capacity)
	}
	for rounds, strategy := range param.Strategies {
		if rounds < 0 {
			return "", fmt.Errorf("strategy round %d must not be negative", rounds)
		}
		if strategy.MMR != nil && (strategy.MMR.MaxSpread < 0 || math.IsNaN(strategy.MMR.MaxSpread) || math.IsInf(strategy.MMR.MaxSpread, 0)) {
			return "", fmt.Errorf("strategy %d MMR max spread must be finite and non-negative", rounds)
		}
		for _, group := range strategy.Required {
			for key, bounds := range group.NumberRanges {
				if !(bounds.Min <= bounds.Max) {
					return "", fmt.Errorf("strategy %d required range %q must have min <= max", rounds, key)
				}
			}
		}
		for _, bonus := range strategy.Bonuses {
			if bonus.Score < 0 || math.IsNaN(bonus.Score) || math.IsInf(bonus.Score, 0) {
				return "", errors.New("bonus score must be finite and non-negative")
			}
			for _, group := range bonus.AnyOf {
				for key, bounds := range group.NumberRanges {
					if !(bounds.Min <= bounds.Max) {
						return "", fmt.Errorf("strategy %d bonus range %q must have min <= max", rounds, key)
					}
				}
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
			MMR:       player.MMR,
			ExtraInfo: player.ExtraInfo,
			TicketId:  ticket.Id,
		}
	}
	ticket.CreateTime = time.Now()
	if param.Timeout > 0 {
		ticket.ExpireTime = ticket.CreateTime.Add(param.Timeout)
	}

	if err := m.matchIndex.add(&ticket); err != nil {
		logCtx.Warnf("添加票索引失败  %v", err)
		return "", err
	}
	logCtx.Info("匹配票入队成功")
	return ticket.Id, nil
}

// Get 按 TicketId 查询仍在队列中的票，返回独立深拷贝；不存在时返回 nil、false。
// 返回值是读取时的快照，之后该票仍可能被并发取消或匹配。
func (m *Matcher[T]) Get(ticketId string) (*Ticket[T], bool) {
	return m.matchIndex.get(ticketId)
}

// GetByIndex 按业务 Index 查询当前排队票，返回值语义与 Get 一致。
func (m *Matcher[T]) GetByIndex(index string) (*Ticket[T], bool) {
	return m.matchIndex.getByIndex(index)
}

func (m *Matcher[T]) Cancel(index string) error {
	logCtx := m.logger.WithField("action", "Cancel").WithField("index", index)
	if index == "" {
		return errors.New("invalid param")
	}

	if err := m.matchIndex.cancel(index); err != nil {
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
	return m.matchIndex.cancelTicket(ticketId)
}
