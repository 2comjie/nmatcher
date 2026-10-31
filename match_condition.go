package nmatcher

type NumberRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type MatchConditions struct {
	StringEquals map[string]string      `json:"string_equals"`
	NumberRanges map[string]NumberRange `json:"number_ranges"`
}

type MatchStrategy struct {
	AnyOf []MatchConditions `json:"any_of"` // 组间为 OR，组内为 AND
}

func (s MatchStrategy) Accept[T PlayerId](other *Ticket[T]) bool {
	if len(s.AnyOf) == 0 {
		return true
	}

	for _, group := range s.AnyOf {
		matched := true

		for key, expected := range group.StringEquals {
			actual, exists := other.StringData[key]
			if !exists || actual != expected {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}

		for key, bounds := range group.NumberRanges {
			actual, exists := other.NumberData[key]
			if !exists || !(actual >= bounds.Min && actual <= bounds.Max) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}

	return false

}
