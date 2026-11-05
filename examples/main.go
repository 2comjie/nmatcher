// 运行：在 nmatcher 目录执行 go run ./examples。
// 演示：票的查询和取消，以及 1 张双人票 + 8 张单人票组成一局 5v5。
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/2comjie/nmatcher"
	"github.com/2comjie/ntool/logx"
	"github.com/2comjie/ntool/logx/logdef"
)

func main() {
	matched := make(chan struct{}, 1)

	// 1. 配置阵营和回调。默认不拆票，双人票中的两人会分配到同一阵营。
	matcher := nmatcher.NewMatcher[uint64](logx.WithLevel(logdef.LevelError),
		nmatcher.WithTeamInfo[uint64](map[nmatcher.Team]int{"red": 5, "blue": 5}),
		nmatcher.WithMatchInterval[uint64](100*time.Millisecond),
		nmatcher.WithMMRBalance[uint64](true),
		nmatcher.WithOnMatched[uint64](func(result nmatcher.MatchResult[uint64]) {
			// 参数是独立副本；可以交给业务创建房间。
			fmt.Printf("\n匹配成功：%s，共 %d 张票\n", result.Id, len(result.TicketMap))
			for _, team := range []nmatcher.Team{"red", "blue"} {
				players := result.TeamMap[team]
				details := make([]string, 0, len(players))
				totalMMR := 0.0
				for _, player := range players {
					details = append(details, fmt.Sprintf("%d(MMR=%.0f)", player.Uid, player.MMR))
					totalMMR += player.MMR
				}
				fmt.Printf("  %s：%s；平均 MMR=%.1f\n", team, strings.Join(details, ", "), totalMMR/float64(len(players)))
			}
			matched <- struct{}{}
		}),
		nmatcher.WithOnTimeout[uint64](func(ticket nmatcher.Ticket[uint64]) {
			fmt.Printf("匹配超时：Index=%s，TicketId=%s\n", ticket.Index, ticket.Id)
		}),
	)

	// 整局 MMR 跨度先限制为 300，等待 10 轮（本例 1 秒）后放宽为 500。
	// 这 10 人的跨度为 450，因此会在放宽后匹配成功；0 阶段必须存在。
	strategies := map[int]nmatcher.MatchStrategy{
		0:  {MMR: &nmatcher.MMRCondition{MaxSpread: 300}},
		10: {MMR: &nmatcher.MMRCondition{MaxSpread: 500}},
	}

	// 2. 演示入队回执、查询和取消。Index 是业务唯一标识，TicketId 标识本次入队。
	ticketId, err := matcher.Add(nmatcher.AddParam[uint64]{
		Index:      "player-99",
		Players:    map[uint64]nmatcher.AddPlayer[uint64]{99: {MMR: 1500}},
		Strategies: strategies,
	})
	if err != nil {
		log.Fatal(err)
	}
	if ticket, exists := matcher.Get(ticketId); exists {
		fmt.Printf("按 TicketId 查询：Index=%s，人数=%d\n", ticket.Index, len(ticket.Players))
	}
	if ticket, exists := matcher.GetByIndex("player-99"); exists {
		fmt.Printf("按 Index 查询：TicketId=%s\n", ticket.Id)
	}
	// 精确取消本次入队；也可使用 matcher.Cancel("player-99") 取消当前业务票。
	if err := matcher.CancelTicket(ticketId); err != nil {
		log.Fatal(err)
	}
	fmt.Println("已取消 player-99，接下来添加正式参与匹配的 10 人。")

	// 3. 一张票可以包含多个玩家。玩家 Id 以 Players 的 map key 为准。
	partyId, err := matcher.Add(nmatcher.AddParam[uint64]{
		Index: "party-1",
		Players: map[uint64]nmatcher.AddPlayer[uint64]{
			1: {MMR: 1850, ExtraInfo: map[string]string{"nickname": "小明"}},
			2: {MMR: 1750, ExtraInfo: map[string]string{"nickname": "小红"}},
		},
		Strategies: strategies,
		Timeout:    10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	for uid := uint64(3); uid <= 10; uid++ {
		if _, err := matcher.Add(nmatcher.AddParam[uint64]{
			Index:      fmt.Sprintf("player-%d", uid), // 每张排队票的业务标识必须唯一。
			Players:    map[uint64]nmatcher.AddPlayer[uint64]{uid: {MMR: 1900 - float64(uid)*50}},
			Strategies: strategies,
			Timeout:    10 * time.Second,
		}); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("已入队 10 人，双人票 TicketId=%s；开始每 100ms 匹配一次。\n", partyId)

	// 4. 启动定时匹配。示例先准备票再启动，方便演示查询；运行中也可以并发入队、取消。
	// 演示完成后自动停止；生产环境将 ctx 接入服务的退出流程即可。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		matcher.Start(ctx)
	}()
	select {
	case <-matched:
		cancel()
		<-stopped
	case <-ctx.Done():
		<-stopped
		log.Fatal("示例等待匹配超时：", ctx.Err())
	}
	_, exists := matcher.Get(partyId)
	fmt.Printf("匹配完成后票已出队，查询 exists=%t。匹配器已停止。\n", exists)
}
