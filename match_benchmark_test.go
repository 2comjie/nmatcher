package nmatcher

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

// benchmarkMatcher 的票均为单人票，含两个字符串属性、一个数值属性。
// selective=true 时每张票只接受相同的唯一 group，用于测稀疏无匹配场景。
func benchmarkMatcher(b *testing.B, count, players int, selective bool) (*Matcher[int], map[string]*Ticket[int]) {
	b.Helper()
	m := NewMatcher[int](logx.WithLevel(logdef.LevelError),
		WithTeamInfo[int](map[Team]int{"a": players / 2, "b": players / 2}),
		WithOnMatched[int](func(MatchResult[int]) {}), WithOnTimeout[int](func(Ticket[int]) {}))
	originals := make(map[string]*Ticket[int], count)
	created := time.Now().Add(-time.Minute)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%08d", i)
		condition := MatchConditions{StringEquals: map[string]string{"region": "asia"}}
		if selective {
			condition.StringEquals = map[string]string{"group": id}
		}
		ticket := &Ticket[int]{Id: id, Index: id, CreateTime: created,
			Players:    map[int]*Player[int]{i: {Uid: i, TicketId: id}},
			StringData: map[string]string{"region": "asia", "group": id},
			NumberData: map[string]float64{"score": float64(i % 1000)},
			Strategies: map[int]MatchStrategy{0: {Required: []MatchConditions{condition}}},
		}
		if err := m.matchIndex.Add(ticket); err != nil {
			b.Fatal(err)
		}
		originals[id] = ticket
	}
	return m, originals
}

// 固定相同的池和调度状态；计时只覆盖 MakeMatch，恢复操作不计时。
func BenchmarkMakeMatch(b *testing.B) {
	for _, count := range []int{10000, 100000} {
		for _, players := range []int{10, 100} {
			b.Run(fmt.Sprintf("N=%d/players=%d", count, players), func(b *testing.B) {
				m, originals := benchmarkMatcher(b, count, players, false)
				now := time.Now()
				seedIds := m.matchIndex.View().Active(now, 1000)
				matches, matchedPlayers := 0, 0
				b.ReportAllocs()
				b.ResetTimer()
				b.StopTimer()
				for i := 0; i < b.N; i++ {
					b.StartTimer()
					results, expired := m.MakeMatch(now)
					b.StopTimer()
					if len(expired) != 0 {
						b.Fatal("unexpected timeout")
					}
					matches += len(results)
					changed := make(map[string]bool, len(seedIds))
					for _, id := range seedIds {
						changed[id] = true
					}
					for _, result := range results {
						for id, ticket := range result.TicketMap {
							changed[id] = true
							matchedPlayers += len(ticket.Players)
						}
					}
					for id := range changed {
						m.matchIndex.Remove(&Ticket[int]{Id: id})
						if err := m.matchIndex.Add(originals[id]); err != nil {
							b.Fatal(err)
						}
					}
				}
				b.ReportMetric(float64(matches)/float64(b.N), "matches/op")
				b.ReportMetric(float64(matchedPlayers)/float64(b.N), "players/op")
			})
		}
	}
	b.Run("N=100000/no-match", func(b *testing.B) {
		m, originals := benchmarkMatcher(b, 100000, 10, true)
		now := time.Now()
		ids := m.matchIndex.View().Active(now, 1000)
		b.ReportAllocs()
		b.ResetTimer()
		b.StopTimer()
		for i := 0; i < b.N; i++ {
			b.StartTimer()
			results, _ := m.MakeMatch(now)
			b.StopTimer()
			if len(results) != 0 {
				b.Fatal("unique groups must not match")
			}
			for _, id := range ids {
				m.matchIndex.Remove(&Ticket[int]{Id: id})
				if err := m.matchIndex.Add(originals[id]); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// 真实 20ms ticker、10 人局，回调通过公开 Add 补回已匹配玩家，维持十万张票。
// round 指标包含 RunOnce 的结果复制、回调及补票，吞吐按包含等待的墙钟时间计算。
func BenchmarkMatchTick20ms(b *testing.B) {
	for _, steps := range []int{10000, 1000, 2000} {
		b.Run(fmt.Sprintf("steps=%d", steps), func(b *testing.B) {
			m, _ := benchmarkMatcher(b, 100000, 10, false)
			m.options.MatchInterval = 20 * time.Millisecond
			m.options.ActiveCooldown = 10 * m.options.MatchInterval
			m.options.MaxSearchStepsPerTick = steps
			matchedPlayers := 0
			m.options.OnMatched = func(result MatchResult[int]) {
				for _, ticket := range result.TicketMap {
					players := make(map[int]AddPlayer[int], len(ticket.Players))
					for uid, player := range ticket.Players {
						players[uid] = AddPlayer[int]{Uid: uid, ExtraInfo: player.ExtraInfo}
					}
					if _, err := m.Add(AddParam[int]{Index: ticket.Index, Players: players,
						StringData: ticket.StringData, NumberData: ticket.NumberData, Strategies: ticket.Strategies}); err != nil {
						b.Fatal(err)
					}
					matchedPlayers += len(players)
				}
			}
			latency := make([]int64, 0, b.N)
			overruns := 0
			// 清掉建池时的原始票副本，基线只保留真实队列和索引。
			runtime.GC()
			var before, after, live runtime.MemStats
			runtime.ReadMemStats(&before)
			ticker := time.NewTicker(m.options.MatchInterval)
			defer ticker.Stop()
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			for i := 0; i < b.N; i++ {
				<-ticker.C
				roundStart := time.Now()
				m.RunOnce()
				elapsed := time.Since(roundStart)
				latency = append(latency, elapsed.Nanoseconds())
				if elapsed > m.options.MatchInterval {
					overruns++
				}
			}
			wallTime := time.Since(started)
			b.StopTimer()
			runtime.ReadMemStats(&after)
			slices.Sort(latency)
			b.ReportMetric(float64(matchedPlayers)/wallTime.Seconds(), "players/s")
			b.ReportMetric(float64(matchedPlayers)/float64(b.N), "players/round")
			b.ReportMetric(float64(b.N)/wallTime.Seconds(), "ticks/s")
			b.ReportMetric(float64(latency[len(latency)/2]), "round-p50-ns")
			b.ReportMetric(float64(latency[(len(latency)-1)*99/100]), "round-p99-ns")
			b.ReportMetric(float64(latency[len(latency)-1]), "round-max-ns")
			b.ReportMetric(float64(overruns), "overruns")
			b.ReportMetric(float64(after.NumGC-before.NumGC)/wallTime.Seconds(), "gc/s")
			b.ReportMetric(float64(after.PauseTotalNs-before.PauseTotalNs)/1e6/wallTime.Seconds(), "gc-pause-ms/s")
			b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/1e6, "total-alloc-MB")
			b.ReportMetric(float64(before.HeapAlloc)/1e6, "heap-start-MB")
			b.ReportMetric(float64(after.HeapAlloc)/1e6, "heap-end-MB")
			// 单独测保留十万票时的存活堆；强制 GC 不计入匹配耗时和 GC 速率。
			runtime.GC()
			runtime.ReadMemStats(&live)
			b.ReportMetric(float64(live.HeapAlloc)/1e6, "heap-live-MB")
			b.ReportMetric(float64(live.HeapInuse)/1e6, "heap-inuse-MB")
			b.ReportMetric(float64(live.Sys)/1e6, "runtime-sys-MB")
			if m.matchIndex.View().tickets.Len() != 100000 {
				b.Fatal("replenishment must preserve the ticket count")
			}
		})
	}
}

// 八核运行时有八个并发请求方，比较空闲、无匹配和成功匹配的 100ms 后台 tick。
// 成功匹配场景从十万张票开始；请求票的属性与存量票不兼容，确保请求能正常取消。
// ns/op 是聚合吞吐成本，p50/p99 是单次 Add+Cancel 的实际墙钟延迟。
func BenchmarkAddCancelDuringMatch(b *testing.B) {
	for _, tc := range []struct {
		name       string
		background bool
		selective  bool
		interval   time.Duration
	}{
		{"Idle", false, true, 100 * time.Millisecond},
		{"Tick100ms", true, true, 100 * time.Millisecond},
		{"Matching100ms", true, false, 100 * time.Millisecond},
		{"Matching20ms", true, false, 20 * time.Millisecond},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m, _ := benchmarkMatcher(b, 100000, 10, tc.selective)
			m.options.MatchInterval = tc.interval
			m.options.ActiveCooldown = 10 * tc.interval
			var matchedPlayers atomic.Int64
			m.options.OnMatched = func(result MatchResult[int]) {
				for _, ticket := range result.TicketMap {
					matchedPlayers.Add(int64(len(ticket.Players)))
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			if tc.background {
				go func() { defer close(done); m.Start(ctx) }()
			} else {
				close(done)
			}
			var next atomic.Int64
			var mu sync.Mutex
			var latency []int64
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			b.RunParallel(func(pb *testing.PB) {
				var local []int64
				for pb.Next() {
					uid := 100000 + int(next.Add(1))
					index := "request-" + strconv.Itoa(uid)
					start := time.Now()
					_, err := m.Add(AddParam[int]{Index: index, Players: map[int]AddPlayer[int]{uid: {Uid: uid}}, StringData: map[string]string{"group": index}, Strategies: map[int]MatchStrategy{0: {Required: []MatchConditions{{StringEquals: map[string]string{"group": index}}}}}})
					if err != nil {
						b.Error(err)
						return
					}
					if err := m.Cancel(index); err != nil {
						b.Error(err)
						return
					}
					local = append(local, time.Since(start).Nanoseconds())
				}
				mu.Lock()
				latency = append(latency, local...)
				mu.Unlock()
			})
			b.StopTimer()
			cancel()
			<-done
			wallTime := time.Since(started)
			slices.Sort(latency)
			if len(latency) != 0 {
				b.ReportMetric(float64(latency[len(latency)/2]), "p50-ns")
				b.ReportMetric(float64(latency[(len(latency)-1)*99/100]), "p99-ns")
			}
			b.ReportMetric(float64(m.frameIndex.Load()), "ticks")
			b.ReportMetric(float64(matchedPlayers.Load()), "matched-players")
			b.ReportMetric(float64(m.frameIndex.Load())/wallTime.Seconds(), "ticks/s")
			b.ReportMetric(float64(matchedPlayers.Load())/wallTime.Seconds(), "players/s")
		})
	}
}
