package nmatcher

// SearchMMR 在 seed 附近按票平均 MMR 距离由近到远返回候选 Id。
// seed 应来自入队回执查询或同一视图；其 MMR 汇总由 MatchIndex.Add 生成。
// 只做粗筛，不检查属性、对方策略或完整对局跨度，seed 自身也计入扫描预算。
func (v *MatchView[T]) SearchMMR(seed *Ticket[T], maxSpread float64, maxScanned int) []string {
	return v.searchMMR(seed, maxSpread, maxScanned, nil)
}

// searchMMR 复用输出缓冲区，两个只读迭代器各保留一个前瞻项，合并近邻。
// 超出预算后不再扫描；属性不匹配的票同样消耗预算，不能靠过滤绕过预算。
func (v *MatchView[T]) searchMMR(seed *Ticket[T], maxSpread float64, maxScanned int, result []string) []string {
	result = result[:0]
	if maxScanned <= 0 || v.mmrIndex == nil || seed.MMRMax-seed.MMRMin > maxSpread {
		return result
	}
	// 合法整局的每个玩家都必须落在此区间内，票平均值也必然在其中。
	lower, upper := seed.MMRMax-maxSpread, seed.MMRMin+maxSpread
	pivot := numberIndexItem{Value: seed.MMRAverage}
	right, left := v.mmrIndex.Iter(), v.mmrIndex.Iter()
	defer right.Release()
	defer left.Release()
	hasRight := right.Seek(pivot)
	hasLeft := left.Seek(pivot)
	if hasLeft {
		hasLeft = left.Prev()
	} else {
		hasLeft = left.Last()
	}
	for len(result) < maxScanned {
		if hasLeft && left.Item().Value < lower {
			hasLeft = false
		}
		if hasRight && right.Item().Value > upper {
			hasRight = false
		}
		if !hasLeft && !hasRight {
			break
		}
		if hasRight && (!hasLeft || right.Item().Value-seed.MMRAverage <= seed.MMRAverage-left.Item().Value) {
			result = append(result, right.Item().TicketId)
			if len(result) < maxScanned {
				hasRight = right.Next()
			}
		} else {
			result = append(result, left.Item().TicketId)
			if len(result) < maxScanned {
				hasLeft = left.Prev()
			}
		}
	}
	return result
}
