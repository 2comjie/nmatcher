package nmatcher

import "time"

// beginAttempt 是短写事务：领取到期票、更新计数并安排下次搜索。
// 只替换 Ticket 记录，不修改旧视图共享的玩家、属性和策略。
func (m *MatchIndex[T]) beginAttempt(ticketId string, now time.Time, interval, cooldown time.Duration, maxAttempts int) (*Ticket[T], bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查票过期
	ticket, exists := m.tickets.Get(ticketId)
	if !exists || ticket.NextAttemptAt.After(now) {
		return nil, false
	}
	if !ticket.ExpireTime.IsZero() && !now.Before(ticket.ExpireTime) {
		return nil, false
	}

	// 当前的匹配策略
	rounds := int(now.Sub(ticket.CreateTime) / interval)
	stage := 0
	for after := range ticket.Strategies {
		if after <= rounds && after > stage {
			stage = after
		}
	}

	// 更新这次使用的匹配策略阶段 以及 当前阶段内 主动发起匹配的次数
	updated := *ticket
	if updated.ActiveStage != stage {
		updated.ActiveStage = stage
		updated.ActiveAttempts = 0 // 重置当前阶段重试批次内的尝试次数
	}
	updated.ActiveAttempts++
	next := now.Add(interval) // 下次主动搜索匹配的时间 默认是下个匹配tick

	// 如果当前的匹配策略内 主动发起的次数已经达到上限了
	if updated.ActiveAttempts >= maxAttempts {
		next = now.Add(cooldown) // 休眠一段时间 不主动发起匹配
		updated.ActiveAttempts = 0
	}

	// 放宽条件时必须重新主动搜索，即使尚未到冷却结束时间。
	for after := range ticket.Strategies {
		if after > stage {
			at := ticket.CreateTime.Add(time.Duration(after) * interval)
			if at.Before(next) {
				next = at
			}
		}
	}
	m.activeIndex.Delete(ticket)
	updated.NextAttemptAt = next      // 更新下次主动搜索匹配的时间
	updated.Attempts++                // 总共发起了多少轮匹配了
	m.tickets.Set(ticketId, &updated) // 更新
	m.activeIndex.Set(&updated)
	return &updated, true
}
