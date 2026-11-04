package nmatcher_test

import (
	"errors"
	"testing"
	"time"

	"github.com/2comjie/nmatcher"
	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

func TestMatcherReceiptAndQueries(t *testing.T) {
	var matched nmatcher.MatchResult[int]
	m := nmatcher.NewMatcher[int](logx.WithLevel(logdef.LevelError),
		nmatcher.WithTeamInfo[int](map[nmatcher.Team]int{"a": 1, "b": 1}),
		nmatcher.WithOnMatched[int](func(result nmatcher.MatchResult[int]) { matched = result }),
		nmatcher.WithOnTimeout[int](func(nmatcher.Ticket[int]) {}))
	param := nmatcher.AddParam[int]{Index: "party", Timeout: time.Minute,
		Players:    map[int]nmatcher.AddPlayer[int]{1: {ExtraInfo: map[string]string{"name": "alice"}}},
		StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100},
		Strategies: map[int]nmatcher.MatchStrategy{0: {Required: []nmatcher.MatchConditions{{StringEquals: map[string]string{"region": "asia"}}}}},
	}
	ticketId, err := m.Add(param)
	if err != nil || ticketId == "" {
		t.Fatalf("missing admission receipt: id=%q err=%v", ticketId, err)
	}
	for _, query := range []struct {
		name string
		get  func(string) (*nmatcher.Ticket[int], bool)
		key  string
	}{
		{"TicketId", m.Get, ticketId},
		{"Index", m.GetByIndex, param.Index},
	} {
		t.Run(query.name, func(t *testing.T) {
			ticket, exists := query.get(query.key)
			if !exists || ticket.Id != ticketId || ticket.Index != param.Index || ticket.Players[1].TicketId != ticketId {
				t.Fatal("query did not return the admitted ticket")
			}
			if ticket.CreateTime.IsZero() || ticket.ExpireTime.Sub(ticket.CreateTime) != param.Timeout {
				t.Fatal("query lost ticket timestamps")
			}
			ticket.StringData["region"] = "changed"
			ticket.NumberData["score"] = 999
			ticket.Players[1].ExtraInfo["name"] = "changed"
			ticket.Strategies[0].Required[0].StringEquals["region"] = "changed"
			for _, get := range []func() (*nmatcher.Ticket[int], bool){
				func() (*nmatcher.Ticket[int], bool) { return m.Get(ticketId) },
				func() (*nmatcher.Ticket[int], bool) { return m.GetByIndex(param.Index) },
			} {
				again, exists := get()
				if !exists || again.StringData["region"] != "asia" || again.NumberData["score"] != 100 ||
					again.Players[1].ExtraInfo["name"] != "alice" || again.Strategies[0].Required[0].StringEquals["region"] != "asia" {
					t.Fatal("query result mutated internal ticket data")
				}
			}
		})
	}
	if err := m.CancelTicket(ticketId); err != nil {
		t.Fatal(err)
	}
	newId, err := m.Add(param)
	if err != nil || newId == "" || newId == ticketId {
		t.Fatalf("requeue must issue a new receipt: id=%q err=%v", newId, err)
	}
	if ticket, exists := m.Get(ticketId); exists || ticket != nil {
		t.Fatal("canceled ticket is still visible")
	}
	if err := m.CancelTicket(ticketId); !errors.Is(err, nmatcher.ErrTicketNotFound) {
		t.Fatal("stale receipt must not cancel the new admission", err)
	}
	if current, exists := m.GetByIndex(param.Index); !exists || current.Id != newId {
		t.Fatal("business lookup must resolve the new admission")
	}
	if _, err := m.Add(nmatcher.AddParam[int]{Index: "opponent", Players: map[int]nmatcher.AddPlayer[int]{2: {}},
		StringData: map[string]string{"region": "asia"}, Strategies: map[int]nmatcher.MatchStrategy{0: {}}}); err != nil {
		t.Fatal(err)
	}
	m.RunOnce()
	if matched.TicketMap[newId] == nil || matched.TicketMap[ticketId] != nil {
		t.Fatal("match must contain the new admission")
	}
	if ticket, exists := m.Get(newId); exists || ticket != nil {
		t.Fatal("matched ticket is still visible")
	}
	if ticket, exists := m.GetByIndex(param.Index); exists || ticket != nil {
		t.Fatal("matched business index is still visible")
	}
}

func TestMatcherFailedAddHasNoReceipt(t *testing.T) {
	m := nmatcher.NewMatcher[int](logx.WithLevel(logdef.LevelError),
		nmatcher.WithTeamInfo[int](map[nmatcher.Team]int{"a": 2}),
		nmatcher.WithOnMatched[int](func(nmatcher.MatchResult[int]) {}),
		nmatcher.WithOnTimeout[int](func(nmatcher.Ticket[int]) {}))
	param := nmatcher.AddParam[int]{Index: "party", Players: map[int]nmatcher.AddPlayer[int]{1: {}}, Strategies: map[int]nmatcher.MatchStrategy{0: {}}}
	id, err := m.Add(param)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		param nmatcher.AddParam[int]
		want  error
	}{
		{"invalid request", nmatcher.AddParam[int]{}, nil},
		{"duplicate index", param, nmatcher.ErrIndexDuplicated},
		{"duplicate player", nmatcher.AddParam[int]{Index: "other", Players: param.Players, Strategies: param.Strategies}, nmatcher.ErrPlayerDuplicated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt, err := m.Add(tc.param)
			if receipt != "" || err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("failed admission returned id=%q err=%v", receipt, err)
			}
			if current, exists := m.GetByIndex(param.Index); !exists || current.Id != id {
				t.Fatal("failed admission changed the queued ticket")
			}
		})
	}
	if ticket, exists := m.Get("missing"); exists || ticket != nil {
		t.Fatal("missing ticket lookup must return nil, false")
	}
	if ticket, exists := m.GetByIndex("missing"); exists || ticket != nil {
		t.Fatal("missing business index lookup must return nil, false")
	}
}
