package nmatcher

import "time"

type Matcher[T PlayerId] struct {
}

func (m *Matcher[T]) MakeMatch(now time.Time) []*MatchResult[T] {
	// 1. 选一张排队的票作为seed
	// 2. 收集符合条件的候选票
	// 3. 尝试组合票 同时分配阵营
	//  - 成功 移除本局所有票 记录MatchResult

}
