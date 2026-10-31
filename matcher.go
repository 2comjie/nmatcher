package nmatcher

import (
	"context"
	"time"

	"github.com/2comjie/ntool/logx/logdef"
)

type Matcher[T PlayerId] struct {
	logdef.ILogger
	options *options[T]

	frameIndex int
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
	logCtx := m.WithField("action", "RunOnce").
		WithField("frameIndex", m.frameIndex)

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

func (m *Matcher[T]) MakeMatch(now time.Time) ([]*MatchResult[T], []*Ticket[T]) {
	// 1. 选一张排队的票作为seed
	// 2. 收集符合条件的候选票
	// 3. 尝试组合票 同时分配阵营
	//  - 成功 移除本局所有票 记录MatchResult
}

// CanMatch 判断双方当前策略是否互相接受，不检查阵营容量和分队约束
func (m *Matcher[T]) CanMatch(a, b *Ticket[T], now time.Time) bool {
	aRounds := int(now.Sub(a.CreateTime) / m.options.MatchInterval)
	bRounds := int(now.Sub(b.CreateTime) / m.options.MatchInterval)
	aStrategy := a.StrategyAt(aRounds)
	bStrategy := b.StrategyAt(bRounds)
	return aStrategy.Accept(b) && bStrategy.Accept(a)
}

type AddPlayer[T PlayerId] struct {
	Uid       T
	ExtraInfo map[string]string
}
