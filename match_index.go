package nmatcher

import (
	"cmp"
	"errors"

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

// MatchIndex 的零值可用，由 Matcher.lock 保护。
type MatchIndex[T PlayerId] struct {
	// 完整票与所有辅助索引由同一个组件维护。
	tickets map[string]*Ticket[T]
	// 业务标识 Ticket.Index -> Ticket.Id
	indexToId map[string]string
	// 玩家 Uid -> Ticket.Id
	playerToTicketId map[T]string

	// 属性名 -> 属性值 -> Ticket.Id 集合
	stringIndex map[string]map[string]map[string]struct{}

	// 属性名 -> 按 (Value, TicketId) 排序的 B 树
	numberIndex map[string]*btree.BTreeG[numberIndexItem]
}

func NewMatchIndex[T PlayerId]() *MatchIndex[T] {
	return &MatchIndex[T]{
		tickets:          make(map[string]*Ticket[T]),
		indexToId:        make(map[string]string),
		playerToTicketId: make(map[T]string),
		stringIndex:      make(map[string]map[string]map[string]struct{}),
		numberIndex:      make(map[string]*btree.BTreeG[numberIndexItem]),
	}
}

// Add 检查票 Id、业务标识和玩家冲突，保存独立副本并建立所有索引。
func (m *MatchIndex[T]) Add(ticket *Ticket[T]) error {
	if _, exists := m.tickets[ticket.Id]; exists {
		return ErrTicketDuplicated
	}
	if _, ex := m.indexToId[ticket.Index]; ex {
		return ErrIndexDuplicated
	}
	for uid := range ticket.Players {
		if _, exists := m.playerToTicketId[uid]; exists {
			return ErrPlayerDuplicated
		}
	}

	cloned := ticket.Clone()
	ticket = &cloned
	if m.tickets == nil {
		m.tickets = make(map[string]*Ticket[T])
		m.indexToId = make(map[string]string)
		m.playerToTicketId = make(map[T]string)
		m.stringIndex = make(map[string]map[string]map[string]struct{})
		m.numberIndex = make(map[string]*btree.BTreeG[numberIndexItem])
	}

	m.tickets[ticket.Id] = ticket
	m.indexToId[ticket.Index] = ticket.Id
	for uid := range ticket.Players {
		m.playerToTicketId[uid] = ticket.Id
	}

	for key, value := range ticket.StringData {
		values := m.stringIndex[key]
		if values == nil {
			values = make(map[string]map[string]struct{})
			m.stringIndex[key] = values
		}

		ids := values[value]
		if ids == nil {
			ids = make(map[string]struct{})
			values[value] = ids
		}

		ids[ticket.Id] = struct{}{}
	}

	for key, value := range ticket.NumberData {
		tree := m.numberIndex[key]
		if tree == nil {
			tree = btree.NewBTreeGOptions(
				func(a, b numberIndexItem) bool {
					if order := cmp.Compare(a.Value, b.Value); order != 0 {
						return order < 0
					}
					return a.TicketId < b.TicketId
				},
				btree.Options{NoLocks: true},
			)
			m.numberIndex[key] = tree
		}

		tree.Set(numberIndexItem{
			Value:    value,
			TicketId: ticket.Id,
		})
	}

	return nil
}

// Get 返回票的独立副本。
func (m *MatchIndex[T]) Get(ticketId string) (*Ticket[T], bool) {
	ticket, exists := m.tickets[ticketId]
	if !exists {
		return nil, false
	}
	cloned := ticket.Clone()
	return &cloned, true
}

// GetByIndex 根据业务标识返回票的独立副本。
func (m *MatchIndex[T]) GetByIndex(index string) (*Ticket[T], bool) {
	ticketId, exists := m.indexToId[index]
	if !exists {
		return nil, false
	}
	return m.Get(ticketId)
}

// Remove 按票 Id 找到内部原票，同时删除完整票和所有索引。
// 旧票已移除或被新票替代时不做修改。
func (m *MatchIndex[T]) Remove(ticket *Ticket[T]) {
	stored, exists := m.tickets[ticket.Id]
	if !exists {
		return
	}
	ticket = stored
	delete(m.indexToId, ticket.Index)
	for uid := range ticket.Players {
		delete(m.playerToTicketId, uid)
	}
	for key, value := range ticket.StringData {
		values := m.stringIndex[key]
		ids := values[value]
		delete(ids, ticket.Id)
		if len(ids) == 0 {
			delete(values, value)
		}
		if len(values) == 0 {
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
	delete(m.tickets, ticket.Id)
}

// Search 只按必要条件返回候选票 Id，加分条件不参与筛选。
// 调用方仍需检查完整必要条件和双向接受，再计算得分。
// maxScanned 限制实际遍历的条目数，重复条目也计入预算；不保证返回顺序。
func (m *MatchIndex[T]) Search(strategy MatchStrategy, maxScanned int) []string {
	if maxScanned <= 0 {
		return nil
	}
	var result []string
	seen := make(map[string]struct{})
	scanned := 0
	visit := func(id string) bool {
		scanned++
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			result = append(result, id)
		}
		return scanned < maxScanned
	}

	groups := strategy.Required
	if len(groups) == 0 {
		groups = []MatchConditions{{}}
	}
	for _, group := range groups {
		var ids map[string]struct{}
		possible := true
		for key, value := range group.StringEquals {
			bucket := m.stringIndex[key][value]
			if len(bucket) == 0 {
				possible = false
				break
			}
			if ids == nil || len(bucket) < len(ids) {
				ids = bucket
			}
		}
		if !possible {
			continue
		}

		var tree *btree.BTreeG[numberIndexItem]
		var bounds NumberRange
		for key, limits := range group.NumberRanges {
			candidate := m.numberIndex[key]
			if candidate == nil {
				possible = false
				break
			}
			if tree == nil || candidate.Len() < tree.Len() {
				tree = candidate
				bounds = limits
			}
		}
		if !possible {
			continue
		}

		if ids != nil {
			for id := range ids {
				if !visit(id) {
					return result
				}
			}
		} else if tree != nil {
			tree.Ascend(numberIndexItem{Value: bounds.Min}, func(item numberIndexItem) bool {
				if item.Value > bounds.Max {
					return false
				}
				return visit(item.TicketId)
			})
			if scanned >= maxScanned {
				return result
			}
		} else {
			// 没有正向索引条件时扫描全体票，不等条件由 Accept 检查。
			for id := range m.tickets {
				if !visit(id) {
					return result
				}
			}
			return result
		}
	}
	return result
}
