package nmatcher

import (
	"fmt"
	"time"
)

type options struct {
	TeamInfo         map[Team]int // 阵营信息
	AllowSplitTicket bool         // 是否允许同一张票的玩家分配到不同阵营，默认不允许
	MatchInterval    time.Duration
}

type Option func(*options)

func WithTeamInfo(teamInfo map[Team]int) Option {
	return func(o *options) {
		for k, v := range teamInfo {
			if v <= 0 {
				panic(fmt.Sprintf("invalid team info: %s %d", k, v))
			}
		}
		o.TeamInfo = teamInfo
	}
}

func WithAllowSplitTicket(allow bool) Option {
	return func(o *options) {
		o.AllowSplitTicket = allow
	}
}
