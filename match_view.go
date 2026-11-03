package nmatcher

import "github.com/tidwall/btree"

// MatchView 匹配的只读视图
type MatchView[T PlayerId] struct {
	tickets     *btree.Map[string, *Ticket[T]]
	stringIndex map[string]*btree.BTreeG[stringIndexItem]
	numberIndex map[string]*btree.BTreeG[numberIndexItem]
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
	return v
}

func (v *MatchView[T]) Get(ticketId string) (*Ticket[T], bool) {
	ticket, exists := v.tickets.Get(ticketId)
	if !exists {
		return nil, false
	}
	cloned := ticket.Clone()
	return &cloned, true
}

// Search 返回粗筛候选 Id，扫描预算在 OR 分支间共享，重复条目也消耗预算。
// 调用方仍需检查完整必要条件、双向接受，再评分；不保证排序。
func (v *MatchView[T]) Search(strategy MatchStrategy, maxScanned int) []string {
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
