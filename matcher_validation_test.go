package nmatcher

import (
	"strings"
	"testing"

	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

func TestAddValidatesStrategiesAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		split      bool
		players    int
		strategies map[int]MatchStrategy
		wantError  string
	}{
		{
			name: "negative strategy round", players: 1,
			strategies: map[int]MatchStrategy{0: {}, -1: {}}, wantError: "round",
		},
		{
			name: "inverted required range in later stage", players: 1,
			strategies: map[int]MatchStrategy{0: {}, 5: {Required: []MatchConditions{
				{}, {NumberRanges: map[string]NumberRange{"score": {Min: 200, Max: 100}}},
			}}}, wantError: "required range",
		},
		{
			name: "inverted bonus range", players: 1,
			strategies: map[int]MatchStrategy{0: {Bonuses: []MatchBonus{{Score: 10, AnyOf: []MatchConditions{
				{}, {NumberRanges: map[string]NumberRange{"score": {Min: 200, Max: 100}}},
			}}}}}, wantError: "bonus range",
		},
		{name: "whole party at largest team capacity", players: 3},
		{name: "whole party cannot fit one team", players: 4, wantError: "capacity"},
		{name: "split party at total capacity", split: true, players: 5},
		{name: "split party exceeds total capacity", split: true, players: 6, wantError: "capacity"},
		{
			name: "equal range endpoints and nonnegative stages", players: 1,
			strategies: map[int]MatchStrategy{0: {}, 5: {
				Required: []MatchConditions{{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}}},
				Bonuses:  []MatchBonus{{Score: 10, AnyOf: []MatchConditions{{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}}}}},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 2, "b": 3}),
				WithAllowSplitTicket[int](tc.split),
				WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
			param := AddParam[int]{Index: "party", Players: make(map[int]AddPlayer[int], tc.players),
				StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100},
				Strategies: tc.strategies}
			if param.Strategies == nil {
				param.Strategies = map[int]MatchStrategy{0: {}}
			}
			for uid := 0; uid < tc.players; uid++ {
				param.Players[uid] = AddPlayer[int]{Uid: uid}
			}
			id, err := m.Add(param)
			if tc.wantError == "" {
				if err != nil || id == "" {
					t.Fatalf("valid admission rejected: id=%q err=%v", id, err)
				}
				if ticket, exists := m.Get(id); !exists || len(ticket.Players) != tc.players {
					t.Fatal("admitted ticket lost players")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) || id != "" {
				t.Fatalf("expected %q error and empty receipt, got id=%q err=%v", tc.wantError, id, err)
			}
			if m.matchIndex.tickets.Len() != 0 || len(m.matchIndex.indexToId) != 0 || len(m.matchIndex.playerToTicketId) != 0 ||
				len(m.matchIndex.stringIndex) != 0 || len(m.matchIndex.numberIndex) != 0 || m.matchIndex.activeIndex != nil {
				t.Fatal("invalid admission left tickets or indexes behind")
			}
		})
	}
}
