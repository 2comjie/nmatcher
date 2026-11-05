package nmatcher

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

func addMMRSearchTicket(t *testing.T, index *MatchIndex[int], id int, ratings []float64, strategies map[int]MatchStrategy, created time.Time) *Ticket[int] {
	t.Helper()
	ticketId := fmt.Sprintf("%03d", id)
	ticket := &Ticket[int]{Id: ticketId, Index: ticketId, CreateTime: created, Strategies: strategies,
		Players: make(map[int]*Player[int]), StringData: map[string]string{"kind": fmt.Sprint(id)},
		NumberData: map[string]float64{"mmr": -999}}
	for i, rating := range ratings {
		uid := id*1000 + i
		ticket.Players[uid] = &Player[int]{Uid: uid, MMR: rating, TicketId: ticketId}
	}
	if err := index.Add(ticket); err != nil {
		t.Fatal(err)
	}
	stored, _ := index.Get(ticketId)
	return stored
}

func TestMMRIndexStatsAndSnapshot(t *testing.T) {
	index := NewMatchIndex[int]()
	condition := &MMRCondition{MaxSpread: 500}
	old := addMMRSearchTicket(t, index, 0, []float64{1000, 1200}, map[int]MatchStrategy{0: {MMR: condition}}, time.Now())
	if old.MMRMin != 1000 || old.MMRMax != 1200 || old.MMRAverage != 1100 || old.NumberData["mmr"] != -999 {
		t.Fatal("MMR stats must come from players without changing business NumberData")
	}
	view := index.View()
	condition.MaxSpread = 1
	old.Strategies[0].MMR.MaxSpread = 2
	current, _ := index.Get(old.Id)
	if current.Strategies[0].MMR.MaxSpread != 500 {
		t.Fatal("MMR condition shares memory with input or query result")
	}
	if err := index.CancelTicket(old.Id); err != nil {
		t.Fatal(err)
	}
	newTicket := &Ticket[int]{Id: "new", Index: old.Index, CreateTime: time.Now(), Players: map[int]*Player[int]{0: {Uid: 0, MMR: 1800}},
		MMRMin: -123, MMRMax: -123, MMRAverage: -123}
	if err := index.Add(newTicket); err != nil {
		t.Fatal(err)
	}
	stored, _ := index.Get("new")
	if stored.MMRMin != 1800 || stored.MMRMax != 1800 || stored.MMRAverage != 1800 {
		t.Fatal("caller-provided MMR stats were not replaced")
	}
	if got := view.SearchMMR(current, 500, 10); !slices.Equal(got, []string{old.Id}) {
		t.Fatalf("old MMR view changed: %v", got)
	}
	if got := index.View().SearchMMR(current, 500, 10); len(got) != 0 {
		t.Fatalf("canceled MMR entry remains: %v", got)
	}
	index.Remove(newTicket) // 删除必须使用存储的汇总值，不使用调用方的 -123。
	if index.mmrIndex.Len() != 0 {
		t.Fatal("MMR index was not cleaned")
	}
}

func TestMMRSearchNearestAndBudget(t *testing.T) {
	index := NewMatchIndex[int]()
	var seed *Ticket[int]
	for id, rating := range []float64{1000, 500, 1001, 998, 1003, 1000} {
		ticket := addMMRSearchTicket(t, index, id, []float64{rating}, nil, time.Now())
		if id == 0 {
			seed = ticket
		}
	}
	view := index.View()
	for _, tc := range []struct {
		limit int
		want  []string
	}{
		{0, nil},
		{1, []string{"000"}},
		{3, []string{"000", "005", "002"}},
		{10, []string{"000", "005", "002", "003", "004"}},
	} {
		if got := view.SearchMMR(seed, 10, tc.limit); !slices.Equal(got, tc.want) {
			t.Fatalf("budget=%d: got=%v want=%v", tc.limit, got, tc.want)
		}
	}
	if got := view.SearchMMR(seed, 0, 10); !slices.Equal(got, []string{"000", "005"}) {
		t.Fatalf("zero spread must mean equal MMR: %v", got)
	}
	highest := addMMRSearchTicket(t, index, 6, []float64{3000}, nil, time.Now())
	index.Remove(highest)
	if got := index.View().SearchMMR(highest, 3000, 1); !slices.Equal(got, []string{"004"}) {
		t.Fatalf("query above current index did not search left: %v", got)
	}
}

func TestMMRZeroSpreadForWholeParty(t *testing.T) {
	index := NewMatchIndex[int]()
	party := addMMRSearchTicket(t, index, 0, []float64{1, 1, 1, 1, 1, 1, 1}, nil, time.Now())
	solo := addMMRSearchTicket(t, index, 1, []float64{1}, nil, time.Now())
	if party.MMRAverage != 1 || solo.MMRAverage != 1 || len(index.View().SearchMMR(party, 0, 10)) != 2 {
		t.Fatal("rounding must not exclude equal-MMR parties from zero-spread search")
	}
}

func TestMMRMatchPrefersNearCandidates(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
		WithMaxSeedsPerTick[int](1), WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now().Add(-time.Minute)
	seed := addMMRSearchTicket(t, &m.matchIndex, 0, []float64{1000}, map[int]MatchStrategy{0: {
		MMR:     &MMRCondition{MaxSpread: 300},
		Bonuses: []MatchBonus{{Score: 100000, AnyOf: []MatchConditions{{StringEquals: map[string]string{"kind": "2"}}}}},
	}}, base)
	near := addMMRSearchTicket(t, &m.matchIndex, 1, []float64{1010}, nil, base.Add(time.Nanosecond))
	far := addMMRSearchTicket(t, &m.matchIndex, 2, []float64{1200}, nil, base.Add(2*time.Nanosecond))
	oldView := m.matchIndex.View()
	results, _ := m.MakeMatch(base)
	if len(results) != 1 || results[0].TicketMap[near.Id] == nil || results[0].TicketMap[far.Id] != nil {
		t.Fatal("MMR closeness must take priority over bonuses when enabled")
	}
	if got := m.matchIndex.View().SearchMMR(seed, 300, 10); !slices.Equal(got, []string{far.Id}) {
		t.Fatalf("committed MMR entries remain: %v", got)
	}
	if got := oldView.SearchMMR(seed, 300, 10); len(got) != 3 {
		t.Fatal("matching changed an existing MMR snapshot")
	}
}

func TestMMRWholeGroupAndRelaxation(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1, "c": 1}),
		WithMatchInterval[int](100*time.Millisecond), WithMaxActiveAttempts[int](1), WithActiveCooldown[int](time.Hour),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now().Add(-time.Minute)
	middle := addMMRSearchTicket(t, &m.matchIndex, 0, []float64{1100}, map[int]MatchStrategy{
		0: {MMR: &MMRCondition{MaxSpread: 150}}, 5: {MMR: &MMRCondition{MaxSpread: 300}},
	}, base)
	low := addMMRSearchTicket(t, &m.matchIndex, 1, []float64{1000}, map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: 300}}}, base)
	high := addMMRSearchTicket(t, &m.matchIndex, 2, []float64{1200}, map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: 300}}}, base)
	if !m.CanMatch(middle, low, base) || !m.CanMatch(middle, high, base) || !m.CanMatch(low, high, base) {
		t.Fatal("fixture must be pairwise compatible")
	}
	if results, _ := m.MakeMatch(base); len(results) != 0 {
		t.Fatal("whole group spread 200 exceeds the middle ticket's limit 150")
	}
	after := base.Add(5 * m.options.MatchInterval)
	if got := m.matchIndex.View().Active(after, 10); !slices.Equal(got, []string{middle.Id}) {
		t.Fatalf("MMR stage change must wake the cooling ticket: %v", got)
	}
	if results, _ := m.MakeMatch(after); len(results) != 1 || len(results[0].TicketMap) != 3 {
		t.Fatal("relaxed ticket did not match the passive candidates")
	}
}

func TestMMRBacktrackingRestoresGroupLimit(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1, "c": 1}),
		WithMaxSeedsPerTick[int](1), WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now().Add(-time.Minute)
	for id, rating := range []float64{1000, 1010, 1100, 1200} {
		limit := 300.0
		if id == 1 {
			limit = 20 // 这张最近的票无法与后面任何一张一起凑满一局。
		}
		addMMRSearchTicket(t, &m.matchIndex, id, []float64{rating}, map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: limit}}}, base.Add(time.Duration(id)))
	}
	results, _ := m.MakeMatch(base.Add(time.Second))
	if len(results) != 1 || results[0].TicketMap["001"] != nil || results[0].TicketMap["002"] == nil || results[0].TicketMap["003"] == nil {
		t.Fatal("failed branch must restore its stricter MMR limit before skipping the ticket")
	}
}

func TestMMRScanBudgetIncludesRejectedAttributes(t *testing.T) {
	for _, budget := range []int{2, 3} {
		m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
			WithMaxSeedsPerTick[int](1), WithMaxScanned[int](budget),
			WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
		base := time.Now().Add(-time.Minute)
		addMMRSearchTicket(t, &m.matchIndex, 0, []float64{1000}, map[int]MatchStrategy{0: {
			MMR: &MMRCondition{MaxSpread: 10}, Required: []MatchConditions{{StringNotEquals: map[string]string{"kind": "1"}}},
		}}, base)
		addMMRSearchTicket(t, &m.matchIndex, 1, []float64{1001}, nil, base.Add(time.Nanosecond))
		addMMRSearchTicket(t, &m.matchIndex, 2, []float64{1002}, nil, base.Add(2*time.Nanosecond))
		results, _ := m.MakeMatch(base)
		if budget == 2 && len(results) != 0 {
			t.Fatal("rejected attributes must still consume the MMR scan budget")
		}
		if budget == 3 && (len(results) != 1 || results[0].TicketMap["002"] == nil) {
			t.Fatal("larger scan budget did not reach the compatible ticket")
		}
	}
}

func TestMMRMutualLimitsAndPartyExtremes(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 2, "b": 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now()
	a := addMMRSearchTicket(t, &m.matchIndex, 0, []float64{1000}, map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: 300}}}, base)
	b := addMMRSearchTicket(t, &m.matchIndex, 1, []float64{1200}, map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: 100}}}, base)
	if m.CanMatch(a, b, base) || m.CanMatch(b, a, base) {
		t.Fatal("both tickets must accept the spread")
	}
	party := addMMRSearchTicket(t, &m.matchIndex, 2, []float64{500, 1500}, nil, base)
	if party.MMRAverage != a.MMRAverage || m.CanMatch(a, party, base) {
		t.Fatal("equal averages must not hide party MMR extremes")
	}
}

func TestMMRStageCanDisableLimit(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now().Add(-time.Minute)
	strategies := map[int]MatchStrategy{0: {MMR: &MMRCondition{MaxSpread: 0}}, 2: {}}
	addMMRSearchTicket(t, &m.matchIndex, 0, []float64{1000}, strategies, base)
	addMMRSearchTicket(t, &m.matchIndex, 1, []float64{1001}, strategies, base)
	if results, _ := m.MakeMatch(base); len(results) != 0 {
		t.Fatal("zero spread must not be treated as disabled")
	}
	if results, _ := m.MakeMatch(base.Add(2 * m.options.MatchInterval)); len(results) != 1 {
		t.Fatal("nil MMR at the next stage must remove the old limit")
	}
}

func TestAddValidatesMMRSpread(t *testing.T) {
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	for _, spread := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		id, err := m.Add(AddParam[int]{Index: "party", Players: map[int]AddPlayer[int]{1: {MMR: 1000}},
			Strategies: map[int]MatchStrategy{0: {}, 5: {MMR: &MMRCondition{MaxSpread: spread}}}})
		if err == nil || id != "" || m.matchIndex.tickets.Len() != 0 {
			t.Fatal("invalid future MMR spread must fail before admission")
		}
	}
}
