package nmatcher

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
)

func TestConcurrentAddSameBusinessIndex(t *testing.T) {
	m := NewMatcher[int](logx.WithName("same-index"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	start := make(chan struct{})
	results := make(chan error, 32)
	for uid := 0; uid < 32; uid++ {
		go func(uid int) {
			<-start
			_, err := m.Add(AddParam[int]{Index: "same", Players: map[int]AddPlayer[int]{uid: {Uid: uid}}, Strategies: map[int]MatchStrategy{0: {}}})
			results <- err
		}(uid)
	}
	close(start)
	successes := 0
	for i := 0; i < 32; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrIndexDuplicated) {
			t.Fatal(err)
		}
	}
	if successes != 1 || m.matchIndex.view().tickets.Len() != 1 {
		t.Fatal("same index accepted more than one ticket")
	}
	if err := m.Cancel("same"); err != nil {
		t.Fatal(err)
	}
	if len(m.matchIndex.playerToTicketId) != 0 {
		t.Fatal("rejected Add left player occupancy")
	}
}

func TestConcurrentOverlappingMatchCommits(t *testing.T) {
	for round := 0; round < 32; round++ {
		index := newMatchIndex[int]()
		tickets := make([]*Ticket[int], 3)
		for i := range tickets {
			id := strconv.Itoa(i)
			tickets[i] = &Ticket[int]{Id: id, Index: id, Players: map[int]*Player[int]{i: {Uid: i}}}
			if err := index.add(tickets[i]); err != nil {
				t.Fatal(err)
			}
		}
		left := &MatchResult[int]{Id: "left", TicketMap: map[string]*Ticket[int]{"0": tickets[0], "1": tickets[1]}}
		right := &MatchResult[int]{Id: "right", TicketMap: map[string]*Ticket[int]{"1": tickets[1], "2": tickets[2]}}
		start := make(chan struct{})
		l, r := make(chan bool, 1), make(chan bool, 1)
		go func() { <-start; l <- index.commitMatch(left) }()
		go func() { <-start; r <- index.commitMatch(right) }()
		close(start)
		wonLeft, wonRight := <-l, <-r
		if wonLeft == wonRight {
			t.Fatal("overlapping matches must have exactly one winner")
		}
		remaining := "0"
		if wonLeft {
			remaining = "2"
		}
		if _, exists := index.get(remaining); !exists || index.view().tickets.Len() != 1 || index.activeIndex.Len() != 1 || len(index.playerToTicketId) != 1 {
			t.Fatal("failed commit partially removed valid tickets")
		}
	}
}

func TestConcurrentCancelAndExpire(t *testing.T) {
	for round := 0; round < 32; round++ {
		index := newMatchIndex[int]()
		ticket := &Ticket[int]{Id: "old", Index: "team", Players: map[int]*Player[int]{1: {Uid: 1}}, ExpireTime: time.Now().Add(-time.Second)}
		if err := index.add(ticket); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		c, e := make(chan error, 1), make(chan bool, 1)
		go func() { <-start; c <- index.cancel("team") }()
		go func() { <-start; e <- index.expireTicket("old") }()
		close(start)
		err, expired := <-c, <-e
		if expired {
			if !errors.Is(err, ErrTicketNotFound) {
				t.Fatal("cancel succeeded after expiry")
			}
		} else if err != nil {
			t.Fatal("neither cancel nor expiry succeeded", err)
		}
		if err := index.add(&Ticket[int]{Id: "new", Index: "team", Players: ticket.Players}); err != nil {
			t.Fatal(err)
		}
		if index.expireTicket("old") {
			t.Fatal("stale expiry succeeded")
		}
		if err := index.cancelTicket("old"); !errors.Is(err, ErrTicketNotFound) {
			t.Fatal(err)
		}
		if _, exists := index.get("new"); !exists {
			t.Fatal("old operations removed new ticket")
		}
	}
}

func TestMatchedCallbackCanAddAndCancel(t *testing.T) {
	var m *Matcher[int]
	callback := make(chan error, 1)
	m = NewMatcher[int](logx.WithName("callback-test"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(MatchResult[int]) {
			if _, err := m.Add(AddParam[int]{Index: "again", Players: map[int]AddPlayer[int]{1: {Uid: 1}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
				callback <- err
				return
			}
			callback <- m.Cancel("again")
		}), WithOnTimeout[int](func(Ticket[int]) {}))
	if _, err := m.Add(AddParam[int]{Index: "party", Players: map[int]AddPlayer[int]{1: {Uid: 1}, 2: {Uid: 2}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.RunOnce() }()
	select {
	case err := <-callback:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("business callback blocked on matcher lock")
	}
	<-done
	if m.matchIndex.view().tickets.Len() != 0 {
		t.Fatal("callback cancellation did not finish")
	}
}

func TestConcurrentMakeMatchNoDuplicatePlayers(t *testing.T) {
	m := NewMatcher[int](logx.WithName("parallel-make-match"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	for uid := 0; uid < 20; uid++ {
		if _, err := m.Add(AddParam[int]{Index: strconv.Itoa(uid), Players: map[int]AddPlayer[int]{uid: {Uid: uid}}, Strategies: map[int]MatchStrategy{0: {}}}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan []*MatchResult[int], 4)
	for i := 0; i < 4; i++ {
		go func() { <-start; matches, _ := m.makeMatch(time.Now()); results <- matches }()
	}
	close(start)
	seen := make(map[int]bool)
	matches := 0
	for i := 0; i < 4; i++ {
		for _, result := range <-results {
			matches++
			for _, ticket := range result.TicketMap {
				for uid := range ticket.Players {
					if seen[uid] {
						t.Fatalf("player %d matched by concurrent rounds", uid)
					}
					seen[uid] = true
				}
			}
		}
	}
	if matches != 10 || len(seen) != 20 || m.matchIndex.view().tickets.Len() != 0 {
		t.Fatal("concurrent rounds lost players")
	}
}
