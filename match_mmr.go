package nmatcher

import "slices"

// balanceMMR 只调整已选玩家的阵营，不修改票、玩家数据及硬条件。
// 目标是减小各阵营平均 MMR 的方差，兼容阵营人数不相同的情况。
// 返回 true 时仅替换 result.TeamMap，原 TeamMap 仍可用于业务 Pass 检查。
func (m *Matcher[T]) balanceMMR(result *MatchResult[T], selected []*Ticket[T], placements []int, steps *int) bool {
	if *steps <= 0 || len(result.TeamMap) < 2 {
		return false
	}
	teams := make([]Team, 0, len(result.TeamMap))
	for team := range result.TeamMap {
		teams = append(teams, team)
	}
	slices.Sort(teams)
	capacity := make([]int, len(teams))
	sums := make([]float64, len(teams))
	playerCount := 0
	for i, team := range teams {
		capacity[i] = len(result.TeamMap[team])
		playerCount += capacity[i]
		for _, player := range result.TeamMap[team] {
			sums[i] += player.MMR
		}
	}
	quality := func(totals []float64) float64 {
		mean := 0.0
		for i, total := range totals {
			mean += total / float64(capacity[i])
		}
		mean /= float64(len(teams))
		variance := 0.0
		for i, total := range totals {
			diff := total/float64(capacity[i]) - mean
			variance += diff * diff
		}
		return variance
	}
	bestQuality := quality(sums)
	if bestQuality == 0 {
		return false
	}

	type unit struct {
		Players []*Player[T]
		MMR     float64
		Team    int
	}
	units := make([]unit, 0, playerCount)
	if m.options.AllowSplitTicket {
		// 切片引用原分队中的玩家，不复制玩家，也不修改原切片。
		for team, name := range teams {
			players := result.TeamMap[name]
			for i, player := range players {
				units = append(units, unit{Players: players[i : i+1], MMR: player.MMR, Team: team})
			}
		}
	} else {
		// buildResult 按 selected 顺序将每张票的玩家连续写入所属阵营。
		// 一张票作为一个分配单元，任何搜索与交换都不会将其拆开。
		offsets := make([]int, len(teams))
		for i, ticket := range selected {
			team := placements[i]
			end := offsets[team] + len(ticket.Players)
			players := result.TeamMap[teams[team]][offsets[team]:end]
			offsets[team] = end
			u := unit{Players: players, Team: team}
			for _, player := range players {
				u.MMR += player.MMR
			}
			units = append(units, u)
		}
	}
	assignment := make([]int, len(units))
	for i, u := range units {
		assignment[i] = u.Team
	}

	// 用完整搜索树的节点上界判断能否枚举；计算上界时同时避免整数溢出。
	// 例如 10 个单人单元、2 个阵营，上界为 2047，容量剪枝后更少。
	exact := true
	nodes, width := 1, 1
	for range units {
		if width > (*steps-nodes)/len(teams) {
			exact = false
			break
		}
		width *= len(teams)
		nodes += width
	}
	if exact {
		remaining := slices.Clone(capacity)
		totals := make([]float64, len(teams))
		current := make([]int, len(units))
		var search func(int)
		search = func(pos int) {
			if bestQuality == 0 || *steps <= 0 {
				return
			}
			*steps--
			if pos == len(units) {
				if score := quality(totals); score < bestQuality {
					bestQuality = score
					copy(assignment, current)
				}
				return
			}
			u := units[pos]
			for team, available := range remaining {
				if available < len(u.Players) {
					continue
				}
				remaining[team] -= len(u.Players)
				totals[team] += u.MMR
				current[pos] = team
				search(pos + 1)
				totals[team] -= u.MMR
				remaining[team] += len(u.Players)
			}
		}
		search(0)
	} else {
		// 大局从已有合法分队开始，只接受改善方差的等人数单元交换。
		// 这保证容量、整票约束不变；局部最优不代表全局最优。
		improved := true
		for improved && *steps > 0 && bestQuality > 0 {
			improved = false
			for i := 0; i < len(units) && *steps > 0 && bestQuality > 0; i++ {
				for j := i + 1; j < len(units) && *steps > 0 && bestQuality > 0; j++ {
					*steps-- // 不合法的交换也计入预算，限制实际检查次数。
					left, right := assignment[i], assignment[j]
					if left == right || len(units[i].Players) != len(units[j].Players) {
						continue
					}
					leftSum, rightSum := sums[left], sums[right]
					delta := units[j].MMR - units[i].MMR
					sums[left], sums[right] = leftSum+delta, rightSum-delta
					if score := quality(sums); score < bestQuality {
						bestQuality = score
						assignment[i], assignment[j] = right, left
						improved = true
					} else {
						sums[left], sums[right] = leftSum, rightSum
					}
				}
			}
		}
	}
	changed := false
	for i, u := range units {
		if assignment[i] != u.Team {
			changed = true
			break
		}
	}
	if !changed {
		return false
	}
	balanced := make(map[Team][]*Player[T], len(teams))
	for i, team := range teams {
		balanced[team] = make([]*Player[T], 0, capacity[i])
	}
	for i, u := range units {
		team := teams[assignment[i]]
		balanced[team] = append(balanced[team], u.Players...)
	}
	result.TeamMap = balanced
	return true
}
