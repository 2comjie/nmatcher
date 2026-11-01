package nmatcher

import (
	"maps"
	"slices"
	"time"
)

type Team string

type PlayerId interface {
	~string |
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type Player[T PlayerId] struct {
	Uid       T                 `json:"uid"`
	ExtraInfo map[string]string `json:"extra_info"`
	TicketId  string            `json:"ticket_id"`
}

type Ticket[T PlayerId] struct {
	Id      string           `json:"id"`      // 票的唯一ID
	Index   string           `json:"index"`   // 唯一的标识 由匹配发起方决定 后续匹配发起方可以通过这个唯一标识 取消或者查询匹配信息 一般是队长uid或者队伍id
	Players map[T]*Player[T] `json:"players"` // 这张票里面的所有玩家 单人票就是一个 组队票就是多个

	StringData map[string]string  `json:"string_data"` // 匹配数据
	NumberData map[string]float64 `json:"number_data"` // 匹配数据

	// 等待多少轮后，使用什么匹配策略
	Strategies map[int]MatchStrategy `json:"strategies"`

	CreateTime time.Time `json:"create_time"`
	ExpireTime time.Time `json:"expire_time"`
	Attempts   int       `json:"attempts"` // 该票主动发起匹配的次数
}

// 对局的匹配器
// 比如a阵营4人 b阵营5人 c阵营6人
// 最后的 MatchResult 就是 map[Team]Player
// 根据游戏类型不同 一张票里面的 player 也可能被分到不同的team
// 由业务决定

type MatchResult[T PlayerId] struct {
	Id        string                `json:"id"`
	TeamMap   map[Team][]*Player[T] `json:"team_map"`
	TicketMap map[string]*Ticket[T] `json:"ticket_map"`
}

func (t *Ticket[T]) StrategyAt(rounds int) MatchStrategy {
	selectedRound := 0
	for after := range t.Strategies {
		if after <= rounds && after > selectedRound {
			selectedRound = after
		}
	}
	return t.Strategies[selectedRound]
}

func (t *Ticket[T]) Clone() Ticket[T] {
	cloned := *t

	cloned.StringData = maps.Clone(t.StringData)
	cloned.NumberData = maps.Clone(t.NumberData)

	cloned.Players = maps.Clone(t.Players)
	for uid, player := range t.Players {
		playerCopy := *player
		playerCopy.ExtraInfo = maps.Clone(player.ExtraInfo)
		cloned.Players[uid] = &playerCopy
	}

	cloned.Strategies = maps.Clone(t.Strategies)
	for rounds, strategy := range t.Strategies {
		cloned.Strategies[rounds] = strategy.Clone()
	}

	return cloned
}

func (r *MatchResult[T]) Clone() MatchResult[T] {
	cloned := *r

	cloned.TicketMap = maps.Clone(r.TicketMap)
	for id, ticket := range r.TicketMap {
		ticketCopy := ticket.Clone()
		cloned.TicketMap[id] = &ticketCopy
	}

	cloned.TeamMap = maps.Clone(r.TeamMap)
	for team, players := range r.TeamMap {
		playerCopies := slices.Clone(players)
		for i, player := range players {
			playerCopy := *player
			playerCopy.ExtraInfo = maps.Clone(player.ExtraInfo)
			playerCopies[i] = &playerCopy
		}
		cloned.TeamMap[team] = playerCopies
	}

	return cloned
}
