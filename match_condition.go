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

type MatchStrategy struct {
	Required []MatchConditions `json:"required"` // 必要条件：组间 OR，组内 AND；为空时不限制
	Bonuses  []MatchBonus      `json:"bonuses"`  // 额外加分条件，不影响是否接受
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

// Score 计算单向加分 必要条件失败时返回 0
func (s MatchStrategy) Score[T PlayerId](other *Ticket[T]) float64 {
	if !s.Accept(other) {
		return 0
	}
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
