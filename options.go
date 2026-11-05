package nmatcher

import (
	"fmt"
	"maps"
	"time"
)

type options[T PlayerId] struct {
	TeamInfo                  map[Team]int // 阵营信息
	AllowSplitTicket          bool         // 是否允许同一张票的玩家分配到不同阵营，默认不允许
	MMRBalance                bool         // 根据玩家 MMR 平衡各阵营的平均实力，默认关闭
	MaxMMRBalanceStepsPerTick int          // 每轮共享的平衡搜索预算，0 使用 100000
	MatchInterval             time.Duration
	MaxScanned                int           // 每张 seed 的扫描预算；0 使用 max(100, 阵营总人数*10)
	MaxSeedsPerTick           int           // 每轮发起票上限，0 使用 1000
	MaxSearchStepsPerTick     int           // 每轮组合搜索预算，0 使用 10000
	MaxActiveAttempts         int           // 当前阶段连续尝试次数上限，0 使用 3
	ActiveCooldown            time.Duration // 达到上限后暂停主动搜索的时间，0 使用 10 个匹配周期

	OnMatched OnMatched[T]
	OnTimeout OnTimeout[T]
	Pass      Pass[T]
}

type Option[T PlayerId] func(*options[T])

func WithMatchInterval[T PlayerId](interval time.Duration) Option[T] {
	if interval <= 0 {
		panic("nmatcher: MatchInterval must be positive")
	}
	return func(o *options[T]) { o.MatchInterval = interval }
}

func WithMaxScanned[T PlayerId](limit int) Option[T] {
	if limit < 0 {
		panic("nmatcher: MaxScanned must not be negative")
	}
	return func(o *options[T]) {
		o.MaxScanned = limit
	}
}

func WithTeamInfo[T PlayerId](teamInfo map[Team]int) Option[T] {
	if len(teamInfo) == 0 {
		panic("nmatcher: TeamInfo is required")
	}
	teamInfo = maps.Clone(teamInfo)
	for k, v := range teamInfo {
		if v <= 0 {
			panic(fmt.Sprintf("invalid team info: %s %d", k, v))
		}
	}
	return func(o *options[T]) { o.TeamInfo = maps.Clone(teamInfo) }
}

func WithAllowSplitTicket[T PlayerId](allow bool) Option[T] {
	return func(o *options[T]) { o.AllowSplitTicket = allow }
}

// WithMMRBalance 开启阵营实力平衡，不改变候选检索和必要条件。
func WithMMRBalance[T PlayerId](enabled bool) Option[T] {
	return func(o *options[T]) { o.MMRBalance = enabled }
}

func WithMaxMMRBalanceStepsPerTick[T PlayerId](limit int) Option[T] {
	if limit < 0 {
		panic("nmatcher: MaxMMRBalanceStepsPerTick must not be negative")
	}
	return func(o *options[T]) { o.MaxMMRBalanceStepsPerTick = limit }
}

func WithMaxSeedsPerTick[T PlayerId](limit int) Option[T] {
	if limit < 0 {
		panic("nmatcher: MaxSeedsPerTick must not be negative")
	}
	return func(o *options[T]) { o.MaxSeedsPerTick = limit }
}

func WithMaxSearchStepsPerTick[T PlayerId](limit int) Option[T] {
	if limit < 0 {
		panic("nmatcher: MaxSearchStepsPerTick must not be negative")
	}
	return func(o *options[T]) { o.MaxSearchStepsPerTick = limit }
}

func WithMaxActiveAttempts[T PlayerId](limit int) Option[T] {
	if limit < 0 {
		panic("nmatcher: MaxActiveAttempts must not be negative")
	}
	return func(o *options[T]) { o.MaxActiveAttempts = limit }
}

func WithActiveCooldown[T PlayerId](cooldown time.Duration) Option[T] {
	if cooldown < 0 {
		panic("nmatcher: ActiveCooldown must not be negative")
	}
	return func(o *options[T]) { o.ActiveCooldown = cooldown }
}

func WithOnMatched[T PlayerId](hook OnMatched[T]) Option[T] {
	if hook == nil {
		panic("nmatcher: OnMatched is required")
	}
	return func(o *options[T]) { o.OnMatched = hook }
}

func WithOnTimeout[T PlayerId](hook OnTimeout[T]) Option[T] {
	if hook == nil {
		panic("nmatcher: OnTimeout is required")
	}
	return func(o *options[T]) { o.OnTimeout = hook }
}

// WithPass 设置可选的完整结果检查；nil 表示不附加检查。
func WithPass[T PlayerId](hook Pass[T]) Option[T] {
	return func(o *options[T]) { o.Pass = hook }
}
