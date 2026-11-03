package nmatcher

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/2comjie/ntool/logx/logdef"
)

type Matcher[T PlayerId] struct {
	logdef.ILogger
	options *options[T]

	frameIndex atomic.Int64

	matchIndex MatchIndex[T]
}

func (m *Matcher[T]) Start(ctx context.Context) {
	ticker := time.NewTicker(m.options.MatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RunOnce()
		}
	}
}

func (m *Matcher[T]) RunOnce() {
	logCtx := m.WithField("action", "RunOnce").WithField("frameIndex", m.frameIndex.Load())
	defer func() {
		m.frameIndex.Add(1)
	}()

	logCtx.Info("开始一轮匹配")

	results, timeoutTickets := m.MakeMatch(time.Now())

	for _, result := range results {
		m.options.OnMatched(result.Clone())
	}

	for _, ticket := range timeoutTickets {
		m.options.OnTimeout(ticket.Clone())
	}

	logCtx.Info("一轮匹配完成")
}

func (m *Matcher[T]) MakeMatch(now time.Time) ([]*MatchResult[T], []*Ticket[T]) {
	logCtx := m.WithField("action", "MakeMatch").WithField("frameIndex", m.frameIndex.Load())
	logCtx.Infof("开始MakeMatch")
	panic("TODO")
}

// CanMatch 判断双方当前策略是否互相接受，不检查阵营容量和分队约束
func (m *Matcher[T]) CanMatch(a, b *Ticket[T], now time.Time) bool {
	aRounds := int(now.Sub(a.CreateTime) / m.options.MatchInterval)
	bRounds := int(now.Sub(b.CreateTime) / m.options.MatchInterval)
	aStrategy := a.StrategyAt(aRounds)
	bStrategy := b.StrategyAt(bRounds)
	return aStrategy.Accept(b) && bStrategy.Accept(a)
}

type AddParam[T PlayerId] struct {
	Index      string
	Players    map[T]AddPlayer[T]
	StringData map[string]string
	NumberData map[string]float64
	Strategies map[int]MatchStrategy
	Timeout    time.Duration
}

type AddPlayer[T PlayerId] struct {
	Uid       T
	ExtraInfo map[string]string
}

func (m *Matcher[T]) Add(param AddParam[T]) error {
	logCtx := m.WithField("action", "Add")
	if len(param.Players) == 0 || param.Index == "" || param.Timeout < 0 {
		return errors.New("invalid param")
	}
	if _, exists := param.Strategies[0]; !exists {
		return errors.New("invalid param")
	}
	for _, strategy := range param.Strategies {
		for _, bonus := range strategy.Bonuses {
			if bonus.Score < 0 || math.IsNaN(bonus.Score) || math.IsInf(bonus.Score, 0) {
				return errors.New("bonus score must be finite and non-negative")
			}
		}
	}

	ticket := Ticket[T]{
		Id:         rand.Text(),
		Index:      param.Index,
		StringData: param.StringData,
		NumberData: param.NumberData,
		Strategies: param.Strategies,
	}

	logCtx = logCtx.WithField("id", ticket.Id).WithField("index", ticket.Index)
	ticket.Players = make(map[T]*Player[T], len(param.Players))

	for uid, player := range param.Players {
		ticket.Players[uid] = &Player[T]{
			Uid:       uid,
			ExtraInfo: player.ExtraInfo,
			TicketId:  ticket.Id,
		}
	}
	ticket.CreateTime = time.Now()
	if param.Timeout > 0 {
		ticket.ExpireTime = ticket.CreateTime.Add(param.Timeout)
	}

	if err := m.matchIndex.Add(&ticket); err != nil {
		logCtx.Warnf("添加票索引失败  %v", err)
		return err
	}
	logCtx.Info("匹配票入队成功")
	return nil
}

func (m *Matcher[T]) Cancel(index string) error {
	logCtx := m.WithField("action", "Cancel").WithField("index", index)
	if index == "" {
		return errors.New("invalid param")
	}

	if err := m.matchIndex.Cancel(index); err != nil {
		return err
	}

	logCtx.Infof("取消匹配成功")
	return nil
}

// CancelTicket 精确取消某次入队，旧请求不会取消相同业务 Index 下的新票。
func (m *Matcher[T]) CancelTicket(ticketId string) error {
	if ticketId == "" {
		return errors.New("invalid param")
	}
	return m.matchIndex.CancelTicket(ticketId)
}

func (m *Matcher[T]) CommitMatch(result *MatchResult[T]) bool {
	return m.matchIndex.CommitMatch(result)
}
