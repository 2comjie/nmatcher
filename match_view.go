package nmatcher

import (
	"time"

	"github.com/tidwall/btree"
)

// MatchView 对外提供只读快照；MakeMatch 可在其独占快照上裁剪已提交的票。
// 各次 View 调用持有独立的 COW 树，裁剪不改变其他视图和实时索引。
type MatchView[T PlayerId] struct {
	tickets     *btree.Map[string, *Ticket[T]]
	stringIndex map[string]*btree.BTreeG[stringIndexItem]
	numberIndex map[string]*btree.BTreeG[numberIndexItem]
	mmrIndex    *btree.BTreeG[numberIndexItem]
	activeIndex *btree.BTreeG[*Ticket[T]]
}

func (m *MatchIndex[T]) View() *MatchView[T] {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := &MatchView[T]{
		tickets:     m.tickets.Copy(),
		stringIndex: make(map[string]*btree.BTreeG[stringIndexItem], len(m.stringIndex)),
		numberIndex: make(map[string]*btree.BTreeG[numberIndexItem], len(m.numberIndex)),
	}
	for key, tree := range m.stringIndex {
		v.stringIndex[key] = tree.Copy()
	}
	for key, tree := range m.numberIndex {
		v.numberIndex[key] = tree.Copy()
	}
	if m.activeIndex != nil {
		v.activeIndex = m.activeIndex.Copy()
	}
	if m.mmrIndex != nil {
		v.mmrIndex = m.mmrIndex.Copy()
	}
	return v
}

// Active 按到期时间返回本视图需要主动搜索的票，候选搜索仍使用全票池。
func (v *MatchView[T]) Active(now time.Time, limit int) []string {
	if limit <= 0 || v.activeIndex == nil {
		return nil
	}
	var ids []string
	v.activeIndex.Scan(func(item *Ticket[T]) bool {
		if item.NextAttemptAt.After(now) {
			return false
		}
		ids = append(ids, item.Id)
		return len(ids) < limit
	})
	return ids
}

func (v *MatchView[T]) Get(ticketId string) (*Ticket[T], bool) {
	ticket, exists := v.tickets.Get(ticketId)
	if !exists {
		return nil, false
	}
	cloned := ticket.Clone()
	return &cloned, true
}

// removeMatched 仅用于 MakeMatch 独占的快照，调用时票必须仍在此快照中。
// 使用快照内的原票删除 active 项，因为实时票的调度时间可能已经更新。
func (v *MatchView[T]) removeMatched(ticketId string) {
	ticket, _ := v.tickets.Get(ticketId)
	for key, value := range ticket.StringData {
		tree := v.stringIndex[key]
		tree.Delete(stringIndexItem{Value: value, TicketId: ticketId})
		if tree.Len() == 0 {
			delete(v.stringIndex, key)
		}
	}
	for key, value := range ticket.NumberData {
		tree := v.numberIndex[key]
		tree.Delete(numberIndexItem{Value: value, TicketId: ticketId})
		if tree.Len() == 0 {
			delete(v.numberIndex, key)
		}
	}
	v.activeIndex.Delete(ticket)
	v.mmrIndex.Delete(numberIndexItem{Value: ticket.MMRAverage, TicketId: ticketId})
	v.tickets.Delete(ticketId)
}

// Search 返回粗筛候选 Id，扫描预算在 OR 分支间共享，重复条目也消耗预算。
// 此入口只查询属性索引；启用 MMR 时使用 SearchMMR，再检查完整条件和双向接受。
// 不保证排序。
func (v *MatchView[T]) Search(strategy MatchStrategy, maxScanned int) []string {
	return v.search(strategy, maxScanned, nil)
}

// search 允许 MakeMatch 复用候选缓冲区；外部 Search 仍返回独立切片。
func (v *MatchView[T]) search(strategy MatchStrategy, maxScanned int, result []string) []string {
	result = result[:0]
	if maxScanned <= 0 {
		return result
	}
	// 单个条件组只遍历一个索引，每张票至多出现一次，无需去重 map。
	var seen map[string]struct{}
	if len(strategy.Required) > 1 {
		seen = make(map[string]struct{})
	}
	scanned := 0
	visit := func(id string) bool {
		scanned++
		if seen == nil {
			result = append(result, id)
		} else if _, exists := seen[id]; !exists {
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
		possible := true
		var stringTree *btree.BTreeG[stringIndexItem]
		var stringValue string
		for key, value := range group.StringEquals {
			tree := v.stringIndex[key]
			if tree == nil {
				possible = false
				break
			}
			if stringTree == nil || tree.Len() < stringTree.Len() {
				stringTree = tree
				stringValue = value
			}
		}
		if !possible {
			continue
		}
		var numberTree *btree.BTreeG[numberIndexItem]
		var bounds NumberRange
		for key, limits := range group.NumberRanges {
			tree := v.numberIndex[key]
			if tree == nil {
				possible = false
				break
			}
			if numberTree == nil || tree.Len() < numberTree.Len() {
				numberTree = tree
				bounds = limits
			}
		}
		if !possible {
			continue
		}
		if stringTree != nil {
			stringTree.Ascend(stringIndexItem{Value: stringValue}, func(item stringIndexItem) bool {
				if item.Value != stringValue {
					return false
				}
				return visit(item.TicketId)
			})
		} else if numberTree != nil {
			numberTree.Ascend(numberIndexItem{Value: bounds.Min}, func(item numberIndexItem) bool {
				if item.Value > bounds.Max {
					return false
				}
				return visit(item.TicketId)
			})
		} else {
			v.tickets.Scan(func(id string, _ *Ticket[T]) bool { return visit(id) })
			return result
		}
		if scanned >= maxScanned {
			return result
		}
	}
	return result
}
