package nmatcher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
)

func TestMatchFullRound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		teams map[Team]int
		sizes []int
		split bool
	}{
		{"whole parties", map[Team]int{"a": 4, "b": 5}, []int{3, 2, 2, 2}, false},
		{"split party", map[Team]int{"a": 2, "b": 3}, []int{5}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var results []MatchResult[int]
			m := NewMatcher[int](logx.WithName("match-test"),
				WithTeamInfo[int](tc.teams), WithAllowSplitTicket[int](tc.split),
				WithOnMatched[int](func(result MatchResult[int]) { results = append(results, result) }),
				WithOnTimeout[int](func(ticket Ticket[int]) { t.Errorf("unexpected timeout: %s", ticket.Id) }))
			uid := 0
			for i, size := range tc.sizes {
				players := make(map[int]AddPlayer[int])
				for j := 0; j < size; j++ {
					uid++
					players[uid] = AddPlayer[int]{Uid: uid}
				}
				if _, err := m.Add(AddParam[int]{Index: strconv.Itoa(i), Players: players, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
					t.Fatal(err)
				}
			}
			m.RunOnce()
			if len(results) != 1 || results[0].Id == "" || len(results[0].TicketMap) != len(tc.sizes) {
				t.Fatalf("unexpected results: %v", results)
			}
			seen := make(map[int]bool)
			placement := make(map[string]Team)
			for team, size := range tc.teams {
				if len(results[0].TeamMap[team]) != size {
					t.Fatalf("team %s size mismatch", team)
				}
				for _, player := range results[0].TeamMap[team] {
					if seen[player.Uid] {
						t.Fatal("duplicate player")
					}
					seen[player.Uid] = true
					if previous, exists := placement[player.TicketId]; exists && !tc.split && previous != team {
						t.Fatal("party split unexpectedly")
					}
					placement[player.TicketId] = team
				}
			}
			if len(seen) != uid || m.matchIndex.View().tickets.Len() != 0 || m.matchIndex.activeIndex.Len() != 0 {
				t.Fatal("incomplete match or index cleanup")
			}
			m.RunOnce()
			if len(results) != 1 {
				t.Fatal("duplicate success callback")
			}
			if _, err := m.Add(AddParam[int]{Index: "0", Players: map[int]AddPlayer[int]{1: {Uid: 1}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
				t.Fatalf("players should be reusable after match: %v", err)
			}
		})
	}
}

func TestMakeMatchHundredPlayers(t *testing.T) {
	for _, limit := range []int{100, 0} {
		t.Run(fmt.Sprintf("scan=%d", limit), func(t *testing.T) {
			m := NewMatcher[int](logx.WithName("match-test"),
				WithTeamInfo[int](map[Team]int{"a": 50, "b": 50}), WithMaxScanned[int](limit),
				WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
			base := time.Now().Add(-time.Minute)
			seed := &Ticket[int]{Id: "seed", Index: "seed", CreateTime: base, Players: map[int]*Player[int]{0: {Uid: 0}}, StringData: map[string]string{"kind": "seed"}}
			if err := m.matchIndex.Add(seed); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 199; i++ {
				id, kind := fmt.Sprintf("bad-%03d", i), "bad"
				condition := MatchConditions{StringEquals: map[string]string{"kind": "bad"}}
				if i >= 99 {
					id, kind = fmt.Sprintf("good-%03d", i), "good"
					condition = MatchConditions{StringNotEquals: map[string]string{"kind": "bad"}}
				}
				ticket := &Ticket[int]{Id: id, Index: id, CreateTime: base.Add(time.Nanosecond), Players: map[int]*Player[int]{i + 1: {Uid: i + 1}}, StringData: map[string]string{"kind": kind}, Strategies: map[int]MatchStrategy{0: {Required: []MatchConditions{condition}}}}
				if err := m.matchIndex.Add(ticket); err != nil {
					t.Fatal(err)
				}
			}
			results, _ := m.MakeMatch(time.Now())
			if limit == 100 {
				if len(results) != 0 {
					t.Fatal("fixed budget should not reach enough mutually compatible tickets")
				}
				return
			}
			if len(results) != 1 || len(results[0].TicketMap) != 100 || len(results[0].TeamMap["a"]) != 50 || len(results[0].TeamMap["b"]) != 50 {
				t.Fatal("100-player match failed")
			}
			for _, ticket := range results[0].TicketMap {
				if ticket.StringData["kind"] == "bad" {
					t.Fatal("reverse condition was ignored")
				}
			}
		})
	}
}

// 已提交的票不能继续占据候选窗口；裁剪本轮快照不能改变其他只读视图。
func TestMakeMatchRefillsCandidateWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		strategy MatchStrategy
	}{
		{"all", MatchStrategy{}},
		{"string", MatchStrategy{Required: []MatchConditions{{StringEquals: map[string]string{"region": "asia"}}}}},
		{"number", MatchStrategy{Required: []MatchConditions{{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}}}}},
		{"or", MatchStrategy{Required: []MatchConditions{
			{StringEquals: map[string]string{"region": "asia"}},
			{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMatcher[int](logx.WithName("candidate-window"), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
				WithMaxScanned[int](3), WithMaxSeedsPerTick[int](12),
				WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
			created := time.Now().Add(-time.Minute)
			for i := 0; i < 12; i++ {
				id := fmt.Sprintf("%02d", i)
				if err := m.matchIndex.Add(&Ticket[int]{Id: id, Index: id, CreateTime: created,
					Players:    map[int]*Player[int]{i: {Uid: i, TicketId: id}},
					StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100},
					Strategies: map[int]MatchStrategy{0: tc.strategy}}); err != nil {
					t.Fatal(err)
				}
			}
			before := m.matchIndex.View()
			results, expired := m.MakeMatch(time.Now())
			if len(results) != 6 || len(expired) != 0 || m.matchIndex.View().tickets.Len() != 0 {
				t.Fatalf("candidate window did not advance: matches=%d expired=%d remaining=%d", len(results), len(expired), m.matchIndex.View().tickets.Len())
			}
			seen := make(map[int]bool)
			for _, result := range results {
				for _, players := range result.TeamMap {
					for _, player := range players {
						if seen[player.Uid] {
							t.Fatal("player matched twice")
						}
						seen[player.Uid] = true
					}
				}
			}
			if len(seen) != 12 || len(before.Search(tc.strategy, 100)) != 12 || len(before.Active(time.Now(), 100)) != 12 {
				t.Fatal("players lost or existing snapshot changed")
			}
		})
	}
}

func TestMakeMatchScoresAndPass(t *testing.T) {
	m := NewMatcher[int](logx.WithName("match-test"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	base := time.Now().Add(-time.Minute)
	for i, id := range []string{"seed", "low", "high"} {
		ticket := &Ticket[int]{Id: id, Index: id, CreateTime: base.Add(time.Duration(i) * time.Nanosecond), Players: map[int]*Player[int]{i: {Uid: i}}, StringData: map[string]string{"kind": id}}
		if id == "seed" {
			ticket.Strategies = map[int]MatchStrategy{0: {Bonuses: []MatchBonus{{Score: 50, AnyOf: []MatchConditions{{StringEquals: map[string]string{"kind": "high"}}}}}}}
		}
		if err := m.matchIndex.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}
	passCalls := 0
	m.options.Pass = func(result MatchResult[int]) bool {
		passCalls++
		for _, ticket := range result.TicketMap {
			ticket.StringData["kind"] = "changed"
		}
		return false
	}
	results, _ := m.MakeMatch(time.Now())
	if len(results) != 0 || passCalls == 0 || m.matchIndex.View().tickets.Len() != 3 {
		t.Fatal("Pass rejection must preserve queued tickets")
	}
	m.options.Pass = nil
	results, _ = m.MakeMatch(time.Now().Add(time.Second))
	if len(results) != 1 || results[0].TicketMap["seed"] == nil || results[0].TicketMap["high"] == nil {
		t.Fatal("higher bonus did not take priority")
	}
	if results[0].TicketMap["high"].StringData["kind"] != "high" {
		t.Fatal("Pass mutated internal ticket")
	}
}

func TestMatchTimeoutCallback(t *testing.T) {
	var timedOut []Ticket[int]
	m := NewMatcher[int](logx.WithName("timeout-test"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(result MatchResult[int]) { t.Error("expired ticket must not match") }),
		WithOnTimeout[int](func(ticket Ticket[int]) { timedOut = append(timedOut, ticket) }))
	if err := m.matchIndex.Add(&Ticket[int]{Id: "old", Index: "expired", CreateTime: time.Now().Add(-time.Minute), ExpireTime: time.Now().Add(-time.Second), Players: map[int]*Player[int]{7: {Uid: 7}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(AddParam[int]{Index: "waiting", Players: map[int]AddPlayer[int]{8: {Uid: 8}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal(err)
	}
	m.RunOnce()
	m.RunOnce()
	if len(timedOut) != 1 || timedOut[0].Id != "old" {
		t.Fatal("timeout must notify exactly once")
	}
	if _, err := m.Add(AddParam[int]{Index: "expired", Players: map[int]AddPlayer[int]{7: {Uid: 7}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal("timeout did not release player/index", err)
	}
}

// 并发提交包含同一玩家的票，只允许一张入队。
func TestConcurrentAddSamePlayer(t *testing.T) {
	m := &Matcher[int]{ILogger: logx.WithName("concurrent-add")}
	start := make(chan struct{})
	type added struct {
		index string
		err   error
	}
	results := make(chan added, 32)
	for i := 0; i < 32; i++ {
		go func(i int) {
			<-start
			index := strconv.Itoa(i)
			_, err := m.Add(AddParam[int]{Index: index, Players: map[int]AddPlayer[int]{1: {Uid: 1}}, Strategies: map[int]MatchStrategy{0: {}}})
			results <- added{index: index, err: err}
		}(i)
	}
	close(start)
	winner, successes := "", 0
	for i := 0; i < 32; i++ {
		result := <-results
		if result.err == nil {
			winner = result.index
			successes++
		} else if !errors.Is(result.err, ErrPlayerDuplicated) {
			t.Fatal(result.err)
		}
	}
	if successes != 1 || m.matchIndex.View().tickets.Len() != 1 {
		t.Fatal("同一玩家只能有一张票入队成功")
	}
	if err := m.Cancel(winner); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(AddParam[int]{Index: "retry", Players: map[int]AddPlayer[int]{1: {Uid: 1}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal("取消后玩家占用没有释放", err)
	}
}

// 在真实 100ms ticker 下并发调用业务入队、取消接口，核对最终玩家归属。
func TestMatchConcurrentAddCancel100msTick(t *testing.T) {
	var mu sync.Mutex
	matched := make(map[int]string)
	canceled := make(map[int]bool)
	progress := make(chan struct{}, 1)
	m := NewMatcher[int](logx.WithName("concurrent-match"), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
		WithOnMatched[int](func(result MatchResult[int]) {
			mu.Lock()
			for _, ticket := range result.TicketMap {
				for uid := range ticket.Players {
					if matched[uid] != "" {
						t.Errorf("player %d matched twice", uid)
					}
					matched[uid] = result.Id
				}
			}
			mu.Unlock()
			select {
			case progress <- struct{}{}:
			default:
			}
		}),
		WithOnTimeout[int](func(ticket Ticket[int]) { t.Errorf("unexpected timeout: %s", ticket.Id) }))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); m.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-stopped })
	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for i := 0; i < 40; i++ {
				uid := worker*40 + i
				index := strconv.Itoa(uid)
				if _, err := m.Add(AddParam[int]{Index: index, Players: map[int]AddPlayer[int]{uid: {Uid: uid}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
					t.Error(err)
					return
				}
				if i%2 == 0 {
					err := m.Cancel(index)
					if err == nil {
						mu.Lock()
						canceled[uid] = true
						mu.Unlock()
					} else if !errors.Is(err, ErrTicketNotFound) {
						t.Error(err)
						return
					}
				}
			}
		}(worker)
	}
	close(start)
	workers.Wait()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	fillerAdded := false
	for {
		waiting := m.matchIndex.View().tickets.Len()
		if waiting == 0 {
			break
		}
		// 单人取消与成交竞争可能留下奇数玩家，补一人以验证最后一票也能正常完成。
		if waiting == 1 && !fillerAdded {
			if _, err := m.Add(AddParam[int]{Index: "filler", Players: map[int]AddPlayer[int]{320: {Uid: 320}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
				t.Fatal(err)
			}
			fillerAdded = true
		}
		select {
		case <-progress:
		case <-deadline.C:
			t.Fatal("queued players did not finish matching")
		}
	}
	cancel()
	<-stopped
	mu.Lock()
	defer mu.Unlock()
	for uid := 0; uid < 320; uid++ {
		if canceled[uid] == (matched[uid] != "") {
			t.Fatalf("player %d should be either canceled or matched once", uid)
		}
	}
}
