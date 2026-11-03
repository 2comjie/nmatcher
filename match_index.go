package nmatcher

import (
	"cmp"
	"errors"
	"sync"
	"time"

	"github.com/tidwall/btree"
)

var (
	ErrTicketDuplicated = errors.New("duplicated ticket")
	ErrTicketNotFound   = errors.New("ticket not found")
	ErrIndexDuplicated  = errors.New("duplicated index")
	ErrPlayerDuplicated = errors.New("player already in matching")
)

type numberIndexItem struct {
	Value    float64
	TicketId string
}
type stringIndexItem struct {
	Value    string
	TicketId string
}

// MatchIndex 统一维护不可变的票和关联索引，写事务由 mu 保护。
// 完整票与属性树参与视图；业务、玩家和终态索引只由短写事务访问。
type MatchIndex[T PlayerId] struct {
	mu               sync.Mutex
	tickets          btree.Map[string, *Ticket[T]]
	indexToId        map[string]string
	playerToTicketId map[T]string
	stringIndex      map[string]*btree.BTreeG[stringIndexItem]
	numberIndex      map[string]*btree.BTreeG[numberIndexItem]
}

func NewMatchIndex[T PlayerId]() *MatchIndex[T] { return &MatchIndex[T]{} }

func (m *MatchIndex[T]) Add(ticket *Ticket[T]) error {
	cloned := ticket.Clone()
	ticket = &cloned
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tickets.Get(ticket.Id); exists {
		return ErrTicketDuplicated
	}
	if _, exists := m.indexToId[ticket.Index]; exists {
		return ErrIndexDuplicated
	}
	for uid := range ticket.Players {
		if _, exists := m.playerToTicketId[uid]; exists {
			return ErrPlayerDuplicated
		}
	}
	if m.indexToId == nil {
		m.indexToId = make(map[string]string)
		m.playerToTicketId = make(map[T]string)
		m.stringIndex = make(map[string]*btree.BTreeG[stringIndexItem])
		m.numberIndex = make(map[string]*btree.BTreeG[numberIndexItem])
	}
	for key, value := range ticket.StringData {
		tree := m.stringIndex[key]
		if tree == nil {
			tree = btree.NewBTreeGOptions(func(a, b stringIndexItem) bool {
				if a.Value != b.Value {
					return a.Value < b.Value
				}
				return a.TicketId < b.TicketId
			}, btree.Options{NoLocks: true})
			m.stringIndex[key] = tree
		}
		tree.Set(stringIndexItem{Value: value, TicketId: ticket.Id})
	}
	for key, value := range ticket.NumberData {
		tree := m.numberIndex[key]
		if tree == nil {
			tree = btree.NewBTreeGOptions(func(a, b numberIndexItem) bool {
				if order := cmp.Compare(a.Value, b.Value); order != 0 {
					return order < 0
				}
				return a.TicketId < b.TicketId
			}, btree.Options{NoLocks: true})
			m.numberIndex[key] = tree
		}
		tree.Set(numberIndexItem{Value: value, TicketId: ticket.Id})
	}
	m.tickets.Set(ticket.Id, ticket)
	m.indexToId[ticket.Index] = ticket.Id
	for uid := range ticket.Players {
		m.playerToTicketId[uid] = ticket.Id
	}
	return nil
}

func (m *MatchIndex[T]) Get(ticketId string) (*Ticket[T], bool) {
	m.mu.Lock()
	ticket, exists := m.tickets.Get(ticketId)
	m.mu.Unlock()
	if !exists {
		return nil, false
	}
	cloned := ticket.Clone()
	return &cloned, true
}

func (m *MatchIndex[T]) GetByIndex(index string) (*Ticket[T], bool) {
	m.mu.Lock()
	ticketId, exists := m.indexToId[index]
	var ticket *Ticket[T]
	if exists {
		ticket, _ = m.tickets.Get(ticketId)
	}
	m.mu.Unlock()
	if !exists || ticket == nil {
		return nil, false
	}
	cloned := ticket.Clone()
	return &cloned, true
}

func (m *MatchIndex[T]) Remove(ticket *Ticket[T]) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stored, exists := m.tickets.Get(ticket.Id); exists {
		m.removeLocked(stored)
	}
}

func (m *MatchIndex[T]) removeLocked(ticket *Ticket[T]) {
	delete(m.indexToId, ticket.Index)
	for uid := range ticket.Players {
		delete(m.playerToTicketId, uid)
	}
	for key, value := range ticket.StringData {
		tree := m.stringIndex[key]
		tree.Delete(stringIndexItem{Value: value, TicketId: ticket.Id})
		if tree.Len() == 0 {
			delete(m.stringIndex, key)
		}
	}
	for key, value := range ticket.NumberData {
		tree := m.numberIndex[key]
		tree.Delete(numberIndexItem{Value: value, TicketId: ticket.Id})
		if tree.Len() == 0 {
			delete(m.numberIndex, key)
		}
	}
	m.tickets.Delete(ticket.Id)
}

func (m *MatchIndex[T]) Cancel(index string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticketId, exists := m.indexToId[index]
	if !exists {
		return ErrTicketNotFound
	}
	ticket, _ := m.tickets.Get(ticketId)
	m.removeLocked(ticket)
	return nil
}

func (m *MatchIndex[T]) CancelTicket(ticketId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, exists := m.tickets.Get(ticketId)
	if !exists {
		return ErrTicketNotFound
	}
	m.removeLocked(ticket)
	return nil
}

// CommitMatch 先复核所有原票，再一起移除；失败不影响其他有效票。
func (m *MatchIndex[T]) CommitMatch(result *MatchResult[T]) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if result.Id == "" || len(result.TicketMap) == 0 {
		return false
	}
	now := time.Now()
	for ticketId := range result.TicketMap {
		ticket, exists := m.tickets.Get(ticketId)
		if !exists || (!ticket.ExpireTime.IsZero() && !now.Before(ticket.ExpireTime)) {
			return false
		}
	}
	for ticketId := range result.TicketMap {
		ticket, _ := m.tickets.Get(ticketId)
		m.removeLocked(ticket)
	}
	return true
}

func (m *MatchIndex[T]) ExpireTicket(ticketId string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, exists := m.tickets.Get(ticketId)
	if !exists || ticket.ExpireTime.IsZero() || time.Now().Before(ticket.ExpireTime) {
		return false
	}
	m.removeLocked(ticket)
	return true
}
