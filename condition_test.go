package nmatcher

import "testing"

func TestCond(t *testing.T) {
	strategy := MatchStrategy{
		Required: []MatchConditions{
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

func TestStringNotEquals(t *testing.T) {
	condition := MatchConditions{StringNotEquals: map[string]string{"guild": "blocked"}}
	for _, tc := range []struct {
		name string
		data map[string]string
		want bool
	}{
		{"different", map[string]string{"guild": "other"}, true},
		{"equal", map[string]string{"guild": "blocked"}, false},
		{"missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := condition.Accept(&Ticket[string]{StringData: tc.data}); got != tc.want {
				t.Fatalf("Accept = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchStrategyScore(t *testing.T) {
	strategy := MatchStrategy{
		Required: []MatchConditions{{StringEquals: map[string]string{"region": "asia"}}},
		Bonuses: []MatchBonus{
			{AnyOf: []MatchConditions{
				{StringEquals: map[string]string{"language": "zh"}},
				{StringEquals: map[string]string{"mode": "rank"}},
			}, Score: 10},
			{AnyOf: []MatchConditions{{
				StringNotEquals: map[string]string{"guild": "blocked"},
				NumberRanges:    map[string]NumberRange{"score": {Min: 1400, Max: 1600}},
			}}, Score: 30},
		},
	}
	for _, tc := range []struct {
		name, region, language, mode, guild string
		score                               float64
		accepted                            bool
		want                                float64
	}{
		{"bonuses accumulate, OR counts once", "asia", "zh", "rank", "other", 1500, true, 40},
		{"no bonus still accepted", "asia", "en", "normal", "blocked", 1800, true, 0},
		{"hard condition cannot be offset", "europe", "zh", "rank", "other", 1500, false, 0},
		{"bonus AND must all pass", "asia", "en", "normal", "blocked", 1500, true, 0},
		{"second OR branch", "asia", "en", "rank", "blocked", 1800, true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := &Ticket[string]{
				StringData: map[string]string{"region": tc.region, "language": tc.language, "mode": tc.mode, "guild": tc.guild},
				NumberData: map[string]float64{"score": tc.score},
			}
			if got := strategy.Accept(other); got != tc.accepted {
				t.Fatalf("Accept = %v, want %v", got, tc.accepted)
			}
			if got := strategy.Score(other); got != tc.want {
				t.Fatalf("Score = %v, want %v", got, tc.want)
			}
		})
	}
	strategy.Bonuses = []MatchBonus{{Score: 5}, {AnyOf: []MatchConditions{{}}, Score: 3}}
	if got := strategy.Score(&Ticket[string]{StringData: map[string]string{"region": "asia"}}); got != 8 {
		t.Fatalf("unconditional bonuses: got %v, want 8", got)
	}
}

func TestTicketCloneMatchStrategy(t *testing.T) {
	ticket := &Ticket[string]{Strategies: map[int]MatchStrategy{0: {
		Required: []MatchConditions{{
			StringEquals:    map[string]string{"region": "asia"},
			StringNotEquals: map[string]string{"guild": "blocked"},
			NumberRanges:    map[string]NumberRange{"score": {Min: 100, Max: 200}},
		}},
		Bonuses: []MatchBonus{{AnyOf: []MatchConditions{{
			StringEquals:    map[string]string{"language": "zh"},
			StringNotEquals: map[string]string{"mode": "normal"},
			NumberRanges:    map[string]NumberRange{"level": {Min: 1, Max: 10}},
		}}, Score: 10}},
	}}}
	cloned := ticket.Clone()
	strategy := cloned.Strategies[0]
	strategy.Required[0].StringEquals["region"] = "europe"
	strategy.Required[0].StringNotEquals["guild"] = "changed"
	strategy.Required[0].NumberRanges["score"] = NumberRange{}
	strategy.Bonuses[0].Score = 999
	strategy.Bonuses[0].AnyOf[0].StringEquals["language"] = "en"
	strategy.Bonuses[0].AnyOf[0].StringNotEquals["mode"] = "changed"
	strategy.Bonuses[0].AnyOf[0].NumberRanges["level"] = NumberRange{}

	original := ticket.Strategies[0]
	if original.Required[0].StringEquals["region"] != "asia" ||
		original.Required[0].StringNotEquals["guild"] != "blocked" ||
		original.Required[0].NumberRanges["score"] != (NumberRange{Min: 100, Max: 200}) ||
		original.Bonuses[0].Score != 10 ||
		original.Bonuses[0].AnyOf[0].StringEquals["language"] != "zh" ||
		original.Bonuses[0].AnyOf[0].StringNotEquals["mode"] != "normal" ||
		original.Bonuses[0].AnyOf[0].NumberRanges["level"] != (NumberRange{Min: 1, Max: 10}) {
		t.Fatal("modifying cloned strategy changed original ticket")
	}
}
