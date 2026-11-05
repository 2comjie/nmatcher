package nmatcher

import (
	"fmt"
	"math"
	"testing"

	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

func resultMMRVariance(result MatchResult[int]) float64 {
	mean := 0.0
	averages := make([]float64, 0, len(result.TeamMap))
	for _, players := range result.TeamMap {
		total := 0.0
		for _, player := range players {
			total += player.MMR
		}
		average := total / float64(len(players))
		averages = append(averages, average)
		mean += average
	}
	mean /= float64(len(averages))
	variance := 0.0
	for _, average := range averages {
		variance += (average - mean) * (average - mean)
	}
	return variance
}

func TestMMRBalance(t *testing.T) {
	tenSingles := make([]int, 10)
	for i := range tenSingles {
		tenSingles[i] = 1
	}
	hundredSingles := make([]int, 100)
	hundredRatings := make([]float64, 100)
	for i := range hundredSingles {
		hundredSingles[i], hundredRatings[i] = 1, float64(i)
	}
	squads := make(map[Team]int, 25)
	for i := 0; i < 25; i++ {
		squads[Team(fmt.Sprintf("team-%02d", i))] = 4
	}
	for _, tc := range []struct {
		name     string
		teams    map[Team]int
		sizes    []int
		ratings  []float64
		split    bool
		variance float64 // -1 表示局部搜索，只要求改善，不要求全局最优。
	}{
		{"ten solo players", map[Team]int{"a": 5, "b": 5}, tenSingles, []float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, false, 0.02},
		{"split one ticket", map[Team]int{"a": 5, "b": 5}, []int{10}, []float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, true, 0.02},
		{"whole parties", map[Team]int{"a": 4, "b": 4}, []int{2, 2, 2, 2}, []float64{10, 10, 8, 8, 2, 2, 0, 0}, false, 0},
		{"different capacities", map[Team]int{"a": 1, "b": 2}, []int{1, 1, 1}, []float64{1, 2, 3}, false, 0},
		{"three teams", map[Team]int{"a": 2, "b": 2, "c": 2}, []int{1, 1, 1, 1, 1, 1}, []float64{9, 8, 7, 3, 2, 1}, false, 0},
		{"hundred players in squads", squads, hundredSingles, hundredRatings, false, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(enabled bool) MatchResult[int] {
				var results []MatchResult[int]
				m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](tc.teams),
					WithAllowSplitTicket[int](tc.split), WithMMRBalance[int](enabled),
					WithOnMatched[int](func(result MatchResult[int]) { results = append(results, result) }),
					WithOnTimeout[int](func(Ticket[int]) {}))
				uid := 0
				for party, size := range tc.sizes {
					players := make(map[int]AddPlayer[int], size)
					for i := 0; i < size; i++ {
						players[uid] = AddPlayer[int]{MMR: tc.ratings[uid], ExtraInfo: map[string]string{"mmr": "ignored"}}
						uid++
					}
					if _, err := m.Add(AddParam[int]{Index: fmt.Sprint(party), Players: players, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
						t.Fatal(err)
					}
				}
				m.RunOnce()
				if len(results) != 1 {
					t.Fatalf("expected one match, got %d", len(results))
				}
				seen := make(map[int]bool)
				partyTeams := make(map[string]Team)
				for team, players := range results[0].TeamMap {
					if len(players) != tc.teams[team] {
						t.Fatal("balance changed team capacity")
					}
					for _, player := range players {
						if seen[player.Uid] || player.MMR != tc.ratings[player.Uid] {
							t.Fatal("balance duplicated or changed a player")
						}
						seen[player.Uid] = true
						if previous, exists := partyTeams[player.TicketId]; exists && !tc.split && previous != team {
							t.Fatal("balance split a whole party")
						}
						partyTeams[player.TicketId] = team
					}
				}
				if len(seen) != len(tc.ratings) || m.matchIndex.tickets.Len() != 0 {
					t.Fatal("balance lost players or failed to commit")
				}
				return results[0]
			}
			before, after := resultMMRVariance(run(false)), resultMMRVariance(run(true))
			if after >= before {
				t.Fatalf("MMR balance did not improve: before=%v after=%v", before, after)
			}
			if tc.variance >= 0 && math.Abs(after-tc.variance) > 1e-9 {
				t.Fatalf("small match not optimal: variance=%v want=%v", after, tc.variance)
			}
		})
	}
}

func TestMMRBalanceRespectsPass(t *testing.T) {
	var matched MatchResult[int]
	rejected := 0
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 5, "b": 5}),
		WithAllowSplitTicket[int](true), WithMMRBalance[int](true),
		WithOnMatched[int](func(result MatchResult[int]) { matched = result }), WithOnTimeout[int](func(Ticket[int]) {}),
		WithPass[int](func(result MatchResult[int]) bool {
			for _, player := range result.TeamMap["a"] {
				if player.Uid >= 5 {
					rejected++
					return false
				}
			}
			return true
		}))
	players := make(map[int]AddPlayer[int])
	for uid := 0; uid < 10; uid++ {
		players[uid] = AddPlayer[int]{MMR: float64(10 - uid)}
	}
	if _, err := m.Add(AddParam[int]{Index: "party", Players: players, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal(err)
	}
	m.RunOnce()
	if matched.Id == "" || rejected == 0 {
		t.Fatal("expected balanced proposal rejection followed by a legal match")
	}
	for _, player := range matched.TeamMap["a"] {
		if player.Uid >= 5 {
			t.Fatal("balance overrode the business Pass constraint")
		}
	}
}

func TestMMRBalanceBudgetSharedAcrossMatches(t *testing.T) {
	var results []MatchResult[int]
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 5, "b": 5}),
		WithMMRBalance[int](true), WithMaxMMRBalanceStepsPerTick[int](50),
		WithOnMatched[int](func(result MatchResult[int]) { results = append(results, result) }), WithOnTimeout[int](func(Ticket[int]) {}))
	for uid := 0; uid < 20; uid++ {
		if _, err := m.Add(AddParam[int]{Index: fmt.Sprint(uid), Players: map[int]AddPlayer[int]{uid: {MMR: float64(10 - uid%10)}},
			Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
			t.Fatal(err)
		}
	}
	m.RunOnce()
	if len(results) != 2 || resultMMRVariance(results[0]) >= 12.5 || resultMMRVariance(results[1]) != 12.5 {
		t.Fatal("balance budget must be shared; exhaustion must preserve feasible matches")
	}
}

func TestAddRejectsNonFiniteMMR(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	for _, rating := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		id, err := m.Add(AddParam[int]{Index: "party", Players: map[int]AddPlayer[int]{1: {MMR: rating}}, Strategies: map[int]MatchStrategy{0: {}}})
		if err == nil || id != "" || m.matchIndex.tickets.Len() != 0 {
			t.Fatal("non-finite MMR must be rejected before admission")
		}
	}
}
