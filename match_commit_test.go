package nmatcher

import (
	"errors"
	"testing"
	"time"
)

func TestCancelBeforeMatchCommit(t *testing.T) {
	index := NewMatchIndex[int]()
	a := &Ticket[int]{Id: "a", Index: "team-a", Players: map[int]*Player[int]{1: {Uid: 1}}}
	b := &Ticket[int]{Id: "b", Index: "team-b", Players: map[int]*Player[int]{2: {Uid: 2}}}
	for _, ticket := range []*Ticket[int]{a, b} {
		if err := index.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}
	result := &MatchResult[int]{Id: "match", TicketMap: map[string]*Ticket[int]{a.Id: a, b.Id: b}}
	if err := index.Cancel(a.Index); err != nil {
		t.Fatal(err)
	}
	if index.CommitMatch(result) {
		t.Fatal("取消先完成后，不应提交旧结果")
	}
	if _, exists := index.Get(b.Id); !exists {
		t.Fatal("提交失败不应移除其他有效票")
	}
	if err := index.Cancel(a.Index); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("重复取消应返回不存在：%v", err)
	}
	current := &Ticket[int]{Id: "new-a", Index: a.Index, Players: a.Players}
	if err := index.Add(current); err != nil {
		t.Fatal(err)
	}
	if err := index.CancelTicket(a.Id); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("旧请求应返回不存在：%v", err)
	}
	if index.CommitMatch(result) {
		t.Fatal("新票不能替代旧票参与提交")
	}
	if _, exists := index.Get(current.Id); !exists {
		t.Fatal("旧取消或旧匹配影响了重新入队的新票")
	}
}

func TestMatchCommitBeforeCancel(t *testing.T) {
	index := NewMatchIndex[string]()
	a := &Ticket[string]{Id: "a", Index: "team-a"}
	b := &Ticket[string]{Id: "b", Index: "team-b"}
	for _, ticket := range []*Ticket[string]{a, b} {
		if err := index.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}
	result := &MatchResult[string]{Id: "match-1", TicketMap: map[string]*Ticket[string]{a.Id: a, b.Id: b}}
	if !index.CommitMatch(result) || index.CommitMatch(result) {
		t.Fatal("同一结果必须仅提交成功一次")
	}
	for _, ticket := range []*Ticket[string]{a, b} {
		if err := index.Cancel(ticket.Index); !errors.Is(err, ErrTicketNotFound) {
			t.Fatalf("成交后取消应返回不存在：%v", err)
		}
	}

	if err := index.Cancel("unknown"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("未知票：%v", err)
	}
}

func TestExpireTicket(t *testing.T) {
	index := NewMatchIndex[int]()
	ticket := &Ticket[int]{Id: "expired", Index: "team", ExpireTime: time.Now().Add(-time.Second)}
	if err := index.Add(ticket); err != nil {
		t.Fatal(err)
	}
	if index.CommitMatch(&MatchResult[int]{Id: "match", TicketMap: map[string]*Ticket[int]{ticket.Id: ticket}}) {
		t.Fatal("过期票不应成交")
	}
	if !index.ExpireTicket(ticket.Id) || index.ExpireTicket(ticket.Id) {
		t.Fatal("过期只能提交成功一次")
	}
	if err := index.Cancel(ticket.Index); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("应返回已超时：%v", err)
	}

}

func TestCancelRacesMatchCommit(t *testing.T) {
	for attempt := 0; attempt < 32; attempt++ {
		index := NewMatchIndex[int]()
		ticket := &Ticket[int]{Id: "ticket", Index: "team"}
		if err := index.Add(ticket); err != nil {
			t.Fatal(err)
		}
		result := &MatchResult[int]{Id: "match", TicketMap: map[string]*Ticket[int]{ticket.Id: ticket}}
		start := make(chan struct{})
		cancelResult := make(chan error, 1)
		commitResult := make(chan bool, 1)
		go func() { <-start; cancelResult <- index.Cancel(ticket.Index) }()
		go func() { <-start; commitResult <- index.CommitMatch(result) }()
		close(start)
		canceled := <-cancelResult
		matched := <-commitResult
		if matched {
			if !errors.Is(canceled, ErrTicketNotFound) {
				t.Fatalf("成交成功时取消必须失败：%v", canceled)
			}
		} else if canceled != nil {
			t.Fatalf("取消成功时成交必须失败：%v", canceled)
		}

	}
}
