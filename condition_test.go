package nmatcher

import "testing"

func TestCond(t *testing.T) {
	strategy := MatchStrategy{
		AnyOf: []MatchConditions{
			{
				StringEquals: map[string]string{"region": "asia"},
				NumberRanges: map[string]NumberRange{
					"score": {Min: 1400, Max: 1600},
				},
			},
			{
				StringEquals: map[string]string{"region": "europe"},
				NumberRanges: map[string]NumberRange{
					"score": {Min: 1800, Max: 2000},
				},
			},
		},
	}

	tests := []struct {
		name   string
		region string
		score  float64
		want   bool
	}{
		{"满足第一组", "asia", 1500, true},
		{"满足第二组", "europe", 1900, true},
		{"包含下界", "asia", 1400, true},
		{"包含上界", "asia", 1600, true},
		{"分数超出范围", "asia", 1601, false},
		{"地区不满足", "america", 1500, false},
		{"不同组的条件不能拼起来", "asia", 1900, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			other := &Ticket[string]{
				StringData: map[string]string{
					"region": tt.region,
				},
				NumberData: map[string]float64{
					"score": tt.score,
				},
			}

			if got := strategy.Accept(other); got != tt.want {
				t.Errorf("Accept() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("缺少必需属性", func(t *testing.T) {
		other := &Ticket[string]{
			StringData: map[string]string{"region": "asia"},
		}
		if strategy.Accept(other) {
			t.Fatal("缺少 score，应该拒绝")
		}
	})

	t.Run("空策略不限制", func(t *testing.T) {
		empty := MatchStrategy{}
		if !empty.Accept(&Ticket[string]{}) {
			t.Fatal("空策略应该接受")
		}
	})
}
