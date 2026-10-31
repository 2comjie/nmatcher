package nmatcher

import "time"

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

type NumberRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// MatchConditions 内所有条件必须同时满足。
// 配置了条件的属性必须存在；数值范围包含 Min 和 Max。
type MatchConditions struct {
	StringEquals map[string]string      `json:"string_equals"`
	NumberRanges map[string]NumberRange `json:"number_ranges"`
}

type MatchStrategy struct {
	AnyOf []MatchConditions `json:"any_of"` // 组间为 OR，组内为 AND
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
