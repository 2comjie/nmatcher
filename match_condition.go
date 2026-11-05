package nmatcher

import (
	"maps"
	"slices"
)

type NumberRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type MatchConditions struct {
	StringEquals    map[string]string      `json:"string_equals"`
	StringNotEquals map[string]string      `json:"string_not_equals"`
	NumberRanges    map[string]NumberRange `json:"number_ranges"`
}

// MatchBonus 任意一组命中后加一次分，多条加分规则可以累加。
type MatchBonus struct {
	AnyOf []MatchConditions `json:"any_of"`
	Score float64           `json:"score"`
}

// MMRCondition 限制整局所有玩家的最高与最低 MMR 差，包含同票内的玩家。
// MaxSpread 为 0 时要求所有玩家 MMR 相同；策略中 MMR 为 nil 时不限制。
type MMRCondition struct {
	MaxSpread float64 `json:"max_spread"`
}

type MatchStrategy struct {
	Required []MatchConditions `json:"required"` // 必要条件：组间 OR，组内 AND；为空时不限制
	Bonuses  []MatchBonus      `json:"bonuses"`  // 额外加分条件，不影响是否接受
	MMR      *MMRCondition     `json:"mmr,omitempty"`
}

// Accept 检查一组 AND 条件，配置的属性必须存在，包括不等条件。
func (c MatchConditions) Accept[T PlayerId](other *Ticket[T]) bool {
	for key, expected := range c.StringEquals {
		actual, exists := other.StringData[key]
		if !exists || actual != expected {
			return false
		}
	}
	for key, excluded := range c.StringNotEquals {
		actual, exists := other.StringData[key]
		if !exists || actual == excluded {
			return false
		}
	}
	for key, bounds := range c.NumberRanges {
		actual, exists := other.NumberData[key]
		if !exists || !(actual >= bounds.Min && actual <= bounds.Max) {
			return false
		}
	}
	return true
}

// Accept 检查对方属性条件；需要双方或整局信息的 MMR 跨度由 CanMatch、MakeMatch 检查。
func (s MatchStrategy) Accept[T PlayerId](other *Ticket[T]) bool {
	if len(s.Required) == 0 {
		return true
	}
	for _, group := range s.Required {
		if group.Accept(other) {
			return true
		}
	}
	return false
}

// Score 计算单向加分，属性条件失败时返回 0；双方 MMR 限制由 CanMatch、MakeMatch 判定。
func (s MatchStrategy) Score[T PlayerId](other *Ticket[T]) float64 {
	if !s.Accept(other) {
		return 0
	}
	return s.bonusScore(other)
}

// bonusScore 只计算额外加分，用于已经通过必要条件校验的候选。
func (s MatchStrategy) bonusScore[T PlayerId](other *Ticket[T]) float64 {
	var score float64
	for _, bonus := range s.Bonuses {
		if len(bonus.AnyOf) == 0 {
			score += bonus.Score
			continue
		}
		for _, group := range bonus.AnyOf {
			if group.Accept(other) {
				score += bonus.Score
				break
			}
		}
	}
	return score
}

func (s MatchStrategy) Clone() MatchStrategy {
	if s.MMR != nil {
		mmr := *s.MMR
		s.MMR = &mmr
	}
	s.Required = slices.Clone(s.Required)
	for i := range s.Required {
		group := &s.Required[i]
		group.StringEquals = maps.Clone(group.StringEquals)
		group.StringNotEquals = maps.Clone(group.StringNotEquals)
		group.NumberRanges = maps.Clone(group.NumberRanges)
	}
	s.Bonuses = slices.Clone(s.Bonuses)
	for i := range s.Bonuses {
		bonus := &s.Bonuses[i]
		bonus.AnyOf = slices.Clone(bonus.AnyOf)
		for j := range bonus.AnyOf {
			group := &bonus.AnyOf[j]
			group.StringEquals = maps.Clone(group.StringEquals)
			group.StringNotEquals = maps.Clone(group.StringNotEquals)
			group.NumberRanges = maps.Clone(group.NumberRanges)
		}
	}
	return s
}
