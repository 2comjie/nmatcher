package nmatcher

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
)

func TestActiveScheduleAndStageWakeup(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	index := NewMatchIndex[int]()
	for _, id := range []string{"b", "a", "c"} {
		ticket := &Ticket[int]{Id: id, Index: id, CreateTime: base, Strategies: map[int]MatchStrategy{0: {}, 5: {}}}
		if err := index.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}
	view := index.View()
	if got := view.Active(base, 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("到期顺序：%v", got)
	}
	for second := 0; second < 3; second++ {
		if _, ok := index.beginAttempt("a", base.Add(time.Duration(second)*time.Second), time.Second, 10*time.Second, 3); !ok {
			t.Fatalf("第 %d 次尝试失败", second)
		}
	}
	if got := index.View().Active(base.Add(4*time.Second), 10); slices.Contains(got, "a") {
		t.Fatal("尚未结束冷却")
	}
	if got := index.View().Active(base.Add(5*time.Second), 10); !slices.Contains(got, "a") {
		t.Fatal("阶段变化没有提前唤醒")
	}
	if _, ok := index.beginAttempt("a", base.Add(5*time.Second), time.Second, 10*time.Second, 3); !ok {
		t.Fatal("无法领取新阶段")
	}
	scheduled, _ := index.Get("a")
	if scheduled.ActiveStage != 5 || scheduled.ActiveAttempts != 1 {
		t.Fatal("新阶段没有重置尝试次数")
	}
	if got := index.View().Active(base.Add(6*time.Second), 10); !slices.Contains(got, "a") {
		t.Fatal("新阶段错误地继续冷却")
	}
	if got := view.Active(base, 10); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("更新调度修改了旧视图：%v", got)
	}
	old, _ := view.Get("a")
	current, _ := index.Get("a")
	if old.Attempts != 0 || !old.NextAttemptAt.Equal(base) || old.ActiveStage != -1 || current.Attempts != 4 || !current.NextAttemptAt.Equal(base.Add(6*time.Second)) {
		t.Fatal("尝试计数或视图隔离错误")
	}
}

func TestActiveBudgetFairnessAndCooldown(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	index := NewMatchIndex[int]()
	for _, id := range []string{"a", "b", "c"} {
		if err := index.Add(&Ticket[int]{Id: id, Index: id, CreateTime: base}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := index.beginAttempt("a", base, time.Second, 10*time.Second, 1); !ok {
		t.Fatal("无法领取 a")
	}
	if _, ok := index.beginAttempt("a", base, time.Second, 10*time.Second, 1); ok {
		t.Fatal("同一时刻重复领取")
	}
	if got := index.View().Active(base, 2); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("未处理的到期票被跳过：%v", got)
	}
	if got := index.View().Active(base.Add(9*time.Second), 10); slices.Contains(got, "a") {
		t.Fatal("提前结束冷却")
	}
	if got := index.View().Active(base.Add(10*time.Second), 10); !slices.Contains(got, "a") {
		t.Fatal("最终阶段也必须能在冷却后重试")
	}
}

func TestActiveRemovedWithTicket(t *testing.T) {
	base := time.Now()
	index := NewMatchIndex[int]()
	old := &Ticket[int]{Id: "old", Index: "team", CreateTime: base}
	if err := index.Add(old); err != nil {
		t.Fatal(err)
	}
	view := index.View()
	if err := index.Cancel("team"); err != nil {
		t.Fatal(err)
	}
	if index.activeIndex.Len() != 0 {
		t.Fatal("取消后调度残留")
	}
	if err := index.Add(&Ticket[int]{Id: "new", Index: "team", CreateTime: base}); err != nil {
		t.Fatal(err)
	}
	index.Remove(old)
	if got := index.View().Active(base, 10); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("旧移除影响新调度：%v", got)
	}
	if got := view.Active(base, 10); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("取消影响旧视图：%v", got)
	}
	if err := index.Add(&Ticket[int]{Id: "expired", Index: "expired", CreateTime: base.Add(-time.Hour), ExpireTime: base.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if !index.ExpireTicket("expired") {
		t.Fatal("没有移除过期票")
	}
	if index.activeIndex.Len() != 1 {
		t.Fatal("超时后调度残留")
	}
}

func TestMakeMatchUsesActiveAndPassivePool(t *testing.T) {
	base := time.Now().Add(-time.Minute)
	m := NewMatcher[int](logx.WithName("active-test"), WithTeamInfo[int](map[Team]int{"a": 1, "b": 1}),
		WithMatchInterval[int](time.Second),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	for i, id := range []string{"passive", "active"} {
		ticket := &Ticket[int]{Id: id, Index: id, CreateTime: base, Players: map[int]*Player[int]{i: {Uid: i, TicketId: id}}}
		if err := m.matchIndex.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := m.matchIndex.beginAttempt("passive", base, time.Second, 10*time.Second, 1); !ok {
		t.Fatal("无法进入冷却")
	}
	results, expired := m.MakeMatch(base.Add(time.Second))
	if len(results) != 1 || len(expired) != 0 || len(results[0].TicketMap) != 2 {
		t.Fatal("主动票没有从全池匹配冷却票")
	}
	if results[0].TicketMap["passive"].Attempts != 1 || results[0].TicketMap["active"].Attempts != 1 {
		t.Fatal("被动匹配不应增加主动次数")
	}
	if m.matchIndex.activeIndex.Len() != 0 {
		t.Fatal("成交后 active 未清理")
	}
}

func TestMakeMatchDoesNotRepeatInactiveSeed(t *testing.T) {
	base := time.Now().Add(-time.Minute)
	m := NewMatcher[int](logx.WithName("active-test"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithMatchInterval[int](time.Second), WithMaxActiveAttempts[int](1), WithActiveCooldown[int](10*time.Second),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	if err := m.matchIndex.Add(&Ticket[int]{Id: "a", Index: "a", CreateTime: base, Players: map[int]*Player[int]{1: {Uid: 1}}}); err != nil {
		t.Fatal(err)
	}
	m.MakeMatch(base)
	m.MakeMatch(base.Add(time.Second))
	ticket, _ := m.matchIndex.Get("a")
	if ticket.Attempts != 1 {
		t.Fatal("冷却中仍主动搜索")
	}
	m.MakeMatch(base.Add(10 * time.Second))
	ticket, _ = m.matchIndex.Get("a")
	if ticket.Attempts != 2 {
		t.Fatal("冷却后没有重新搜索")
	}
}

func TestActiveMatchPassDoesNotBlockWrites(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m := NewMatcher[int](logx.WithName("active-test"), WithTeamInfo[int](map[Team]int{"a": 2}),
		WithMatchInterval[int](time.Second),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}),
		WithPass[int](func(result MatchResult[int]) bool {
			once.Do(func() { close(entered); <-release })
			return true
		}))
	for i, id := range []string{"a", "b"} {
		if err := m.matchIndex.Add(&Ticket[int]{Id: id, Index: id, Players: map[int]*Player[int]{i: {Uid: i}}}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []*MatchResult[int], 1)
	go func() { result, _ := m.MakeMatch(time.Now()); done <- result }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("没有进入 Pass")
	}
	writes := make(chan error, 1)
	go func() {
		if err := m.matchIndex.CancelTicket("a"); err != nil {
			writes <- err
			return
		}
		writes <- m.matchIndex.Add(&Ticket[int]{Id: "new", Index: "a", Players: map[int]*Player[int]{0: {Uid: 0}}})
	}()
	select {
	case err := <-writes:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("匹配阻塞写事务")
	}
	close(release)
	select {
	case results := <-done:
		if len(results) != 0 {
			t.Fatal("旧票取消后仍然成交")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("匹配没有退出")
	}
	if _, exists := m.matchIndex.Get("new"); !exists {
		t.Fatal("旧结果影响新票")
	}
}
