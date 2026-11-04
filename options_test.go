package nmatcher

import (
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
)

func TestOptionsTeamIsolationAndCallbacks(t *testing.T) {
	teams := map[Team]int{"a": 1, "b": 1}
	teamOption := WithTeamInfo[int](teams)
	teams["a"] = 99
	matched := 0
	m := NewMatcher[int](logx.WithName("options-test"), teamOption,
		WithAllowSplitTicket[int](true), WithMaxScanned[int](200),
		WithMaxSeedsPerTick[int](20), WithMaxSearchStepsPerTick[int](1000),
		WithMaxActiveAttempts[int](2), WithActiveCooldown[int](time.Second),
		WithOnMatched[int](func(result MatchResult[int]) { matched++ }),
		WithOnTimeout[int](func(ticket Ticket[int]) { t.Error("unexpected timeout") }),
		WithPass[int](func(result MatchResult[int]) bool { return len(result.TicketMap) == 1 }),
	)
	other := NewMatcher[int](logx.WithName("options-other"), teamOption,
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	other.options.TeamInfo["b"] = 99
	if m.options.MatchInterval != 100*time.Millisecond {
		t.Fatal("default tick should be 100ms")
	}
	if _, err := m.Add(AddParam[int]{Index: "party", Players: map[int]AddPlayer[int]{1: {Uid: 1}, 2: {Uid: 2}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal(err)
	}
	m.RunOnce()
	if matched != 1 {
		t.Fatal("map isolation or configured hooks/split failed")
	}
}
