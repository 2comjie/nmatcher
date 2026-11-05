package nmatcher

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func TestMatchViewSurvivesWrites(t *testing.T) {
	index := NewMatchIndex[int]()
	old := &Ticket[int]{Id: "old", Index: "team", Players: map[int]*Player[int]{1: {Uid: 1}}, StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100}}
	if err := index.Add(old); err != nil {
		t.Fatal(err)
	}
	view := index.View()
	if err := index.CancelTicket(old.Id); err != nil {
		t.Fatal(err)
	}
	current := &Ticket[int]{Id: "new", Index: "team", Players: old.Players, StringData: map[string]string{"region": "europe"}, NumberData: map[string]float64{"score": 200}}
	if err := index.Add(current); err != nil {
		t.Fatal(err)
	}
	for _, group := range []MatchConditions{
		{StringEquals: map[string]string{"region": "asia"}},
		{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}},
	} {
		strategy := MatchStrategy{Required: []MatchConditions{group}}
		if got := view.Search(strategy, 100); !slices.Equal(got, []string{"old"}) {
			t.Fatalf("旧视图改变：%v", got)
		}
	}
	if _, exists := view.Get("new"); exists {
		t.Fatal("旧视图看到了新票")
	}
	copy, exists := view.Get("old")
	if !exists {
		t.Fatal("旧视图丢失旧票")
	}
	copy.StringData["region"] = "changed"
	again, _ := view.Get("old")
	if again.StringData["region"] != "asia" {
		t.Fatal("查询副本修改了视图")
	}
}

func TestMatchViewConcurrentWrites(t *testing.T) {
	index := NewMatchIndex[int]()
	for i := 0; i < 20; i++ {
		id := strconv.Itoa(i)
		if err := index.Add(&Ticket[int]{Id: id, Index: id, StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100}}); err != nil {
			t.Fatal(err)
		}
	}
	view := index.View()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			id := fmt.Sprintf("new-%d", i)
			if err := index.Add(&Ticket[int]{Id: id, Index: id, StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": 100}}); err != nil {
				t.Error(err)
				return
			}
			if err := index.CancelTicket(id); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	close(start)
	seed, _ := view.Get("0")
	for i := 0; i < 100; i++ {
		if got := view.SearchMMR(seed, 0, 100); len(got) != 20 {
			t.Errorf("旧视图 MMR 候选数改变：%d", len(got))
		}
		for _, group := range []MatchConditions{
			{StringEquals: map[string]string{"region": "asia"}},
			{NumberRanges: map[string]NumberRange{"score": {Min: 100, Max: 100}}},
		} {
			if got := view.Search(MatchStrategy{Required: []MatchConditions{group}}, 100); len(got) != 20 {
				t.Errorf("旧视图候选数改变：%d", len(got))
			}
		}
	}
	wg.Wait()
}

func BenchmarkMatchView(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			index := NewMatchIndex[int]()
			for i := 0; i < count; i++ {
				id := strconv.Itoa(i)
				if err := index.Add(&Ticket[int]{Id: id, Index: id, StringData: map[string]string{"region": "asia"}, NumberData: map[string]float64{"score": float64(i)}}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view := index.View()
				if view.tickets.Len() != count {
					b.Fatal("视图票数不一致")
				}
			}
		})
	}
}
