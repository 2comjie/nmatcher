package nmatcher

import (
	"fmt"
	"time"
)

type options[T PlayerId] struct {
	TeamInfo         map[Team]int // 阵营信息
	AllowSplitTicket bool         // 是否允许同一张票的玩家分配到不同阵营，默认不允许
	MatchInterval    time.Duration

	OnMatched OnMatched[T]
	OnTimeout OnTimeout[T]
	Pass      Pass[T]
}

type Option[T PlayerId] func(*options[T])

func WithTeamInfo[T PlayerId](teamInfo map[Team]int) Option[T] {
	return func(o *options[T]) {
		for k, v := range teamInfo {
			if v <= 0 {
				panic(fmt.Sprintf("invalid team info: %s %d", k, v))
			}
		}
		o.TeamInfo = teamInfo
	}
}
