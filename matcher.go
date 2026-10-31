package nmatcher

import (
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/2comjie/ntool/logx/logdef"
)

var (
	ErrTicketDuplicated = errors.New("duplicated ticket")
	ErrTicketNotFound   = errors.New("ticket not found")
)

type Matcher[T PlayerId] struct {
	logdef.ILogger
	options *options[T]

	frameIndex int

	mu sync.Mutex

	tickets    map[string]*Ticket[T] // Ticket.Id -> 完整票
	matchIndex MatchIndex
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
	logCtx := m.WithField("action", "RunOnce").WithField("frameIndex", m.frameIndex)
	defer func() {
		m.frameIndex++
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

func (m *Matcher[T]) lock() {
	m.mu.Lock()
}
func (m *Matcher[T]) unlock() {
	m.mu.Unlock()
}

func (m *Matcher[T]) MakeMatch(now time.Time) ([]*MatchResult[T], []*Ticket[T]) {
	logCtx := m.WithField("action", "MakeMatch").WithField("frameIndex", m.frameIndex)
	logCtx.Infof("开始MakeMatch")
	panic("not implemented")
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
	logCtx := m.WithField("action", "Add").WithField("frameIndex", m.frameIndex)
	if len(param.Players) == 0 || param.Index == "" {
		return errors.New("invalid param")
	}
	if param.Timeout < 0 {
		return errors.New("timeout must not be negative")
	}
	if _, exists := param.Strategies[0]; !exists {
		return errors.New("strategy for round 0 is required")
	}

	ticket := Ticket[T]{
		Id:         rand.Text(),
		Index:      param.Index,
		StringData: maps.Clone(param.StringData),
		NumberData: maps.Clone(param.NumberData),
		Strategies: make(map[int]MatchStrategy, len(param.Strategies)),
	}

	logCtx = logCtx.WithField("id", ticket.Id).WithField("index", ticket.Index)
	for rounds, strategy := range param.Strategies {
		strategy.AnyOf = slices.Clone(strategy.AnyOf)

		for i := range strategy.AnyOf {
			group := &strategy.AnyOf[i]
			group.StringEquals = maps.Clone(group.StringEquals)
			group.NumberRanges = maps.Clone(group.NumberRanges)
		}

		ticket.Strategies[rounds] = strategy
	}

	m.lock()
	defer m.unlock()

	if _, ex := m.tickets[ticket.Id]; ex {
		logCtx.Error("冲突的票")
		return ErrTicketDuplicated
	}

	ticket.CreateTime = time.Now()
	if param.Timeout > 0 {
		ticket.ExpireTime = ticket.CreateTime.Add(param.Timeout)
	}

	if err := m.matchIndex.Add(&ticket); err != nil {
		logCtx.Warnf("添加票索引失败  %v", err)
		return err
	}
	if m.tickets == nil {
		m.tickets = make(map[string]*Ticket[T])
	}
	m.tickets[ticket.Id] = &ticket

	logCtx.Info("匹配票入队成功")
	return nil
}
