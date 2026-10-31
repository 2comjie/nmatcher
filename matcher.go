package nmatcher

import (
	"time"
)

type Matcher[T PlayerId] struct {
	options *options
}

func (m *Matcher[T]) MakeMatch(now time.Time) []*MatchResult[T] {
	// 1. 选一张排队的票作为seed
	// 2. 收集符合条件的候选票
	// 3. 尝试组合票 同时分配阵营
	//  - 成功 移除本局所有票 记录MatchResult
	panic("nmatcher: MakeMatch is not implemented")
}

// CanMatch 判断双方当前策略是否互相接受，不检查阵营容量和分队约束
func (m *Matcher[T]) CanMatch(a, b *Ticket[T], now time.Time) bool {
	aRounds := int(now.Sub(a.CreateTime) / m.options.MatchInterval)
	bRounds := int(now.Sub(b.CreateTime) / m.options.MatchInterval)
	aStrategy := a.StrategyAt(aRounds)
	bStrategy := b.StrategyAt(bRounds)
	return aStrategy.Accept(b) && bStrategy.Accept(a)
}
