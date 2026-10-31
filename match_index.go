package nmatcher

import (
	"cmp"
	"errors"

	"github.com/tidwall/btree"
)

var ErrIndexDuplicated = errors.New("duplicated index")

type numberIndexItem struct {
	Value    float64
	TicketId string
}

// MatchIndex 的零值可用，由 Matcher.lock 保护。
type MatchIndex struct {
	// 业务标识 Ticket.Index -> Ticket.Id
	indexToId map[string]string

	// 属性名 -> 属性值 -> Ticket.Id 集合
	stringIndex map[string]map[string]map[string]struct{}

	// 属性名 -> 按 (Value, TicketId) 排序的 B 树
	numberIndex map[string]*btree.BTreeG[numberIndexItem]
}

func NewMatchIndex() *MatchIndex {
	return &MatchIndex{
		indexToId:   make(map[string]string),
		stringIndex: make(map[string]map[string]map[string]struct{}),
		numberIndex: make(map[string]*btree.BTreeG[numberIndexItem]),
	}
}

// Add 在 Matcher 检查票 Id 和业务标识不重复后调用。
// 票的业务标识和属性在移除索引前必须保持不变。
func (m *MatchIndex) Add[T PlayerId](ticket *Ticket[T]) error {
	if _, ex := m.indexToId[ticket.Index]; ex {
		return ErrIndexDuplicated
	}

	if m.indexToId == nil {
		m.indexToId = make(map[string]string)
		m.stringIndex = make(map[string]map[string]map[string]struct{})
		m.numberIndex = make(map[string]*btree.BTreeG[numberIndexItem])
	}

	m.indexToId[ticket.Index] = ticket.Id

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

// Remove 移除仍在索引中的票，使用入队时的原始属性。
func (m *MatchIndex) Remove[T PlayerId](ticket *Ticket[T]) {
	delete(m.indexToId, ticket.Index)
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
}

// Search 返回去重后的候选票 Id，调用方仍需检查完整策略和双向接受。
// maxScanned 限制实际遍历的条目数，重复条目也计入预算；不保证返回顺序。
func (m *MatchIndex) Search(strategy MatchStrategy, maxScanned int) []string {
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

	groups := strategy.AnyOf
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
			// 空条件组接受所有票，之后的 OR 分支无需重复遍历。
			for _, id := range m.indexToId {
				if !visit(id) {
					return result
				}
			}
			return result
		}
	}
	return result
}
