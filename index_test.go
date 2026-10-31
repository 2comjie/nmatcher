package nmatcher

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
)

func TestMatchIndex(t *testing.T) {
	index := NewMatchIndex[string]()

	tickets := map[string]*Ticket[string]{
		"a": {
			Id:         "a",
			Index:      "leader-a",
			StringData: map[string]string{"region": "asia"},
			NumberData: map[string]float64{"score": 1500},
		},
		"b": {
			Id:         "b",
			Index:      "leader-b",
			StringData: map[string]string{"region": "asia"},
			NumberData: map[string]float64{"score": 1500},
		},
		"c": {
			Id:         "c",
			Index:      "leader-c",
			StringData: map[string]string{"region": "europe"},
			NumberData: map[string]float64{"score": 1800},
		},
		"d": {
			Id:    "d",
			Index: "leader-d",
		},
	}

	for _, ticket := range tickets {
		if err := index.Add(ticket); err != nil {
			t.Fatal(err)
		}
	}

	// 重复业务标识不能覆盖原票，也不能留下新属性索引。
	duplicate := &Ticket[string]{
		Id:         "duplicate",
		Index:      "leader-a",
		StringData: map[string]string{"region": "invalid"},
	}
	if err := index.Add(duplicate); !errors.Is(err, ErrIndexDuplicated) {
		t.Fatalf("重复 Index：得到 %v", err)
	}
	if index.indexToId["leader-a"] != "a" {
		t.Fatal("重复 Add 覆盖了原映射")
	}

	tests := []struct {
		name   string
		groups []MatchConditions
		want   []string
	}{
		{
			name: "空策略",
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "字符串等值",
			groups: []MatchConditions{
				{StringEquals: map[string]string{"region": "asia"}},
			},
			want: []string{"a", "b"},
		},
		{
			name: "同分票都保留",
			groups: []MatchConditions{
				{NumberRanges: map[string]NumberRange{
					"score": {Min: 1500, Max: 1500},
				}},
			},
			want: []string{"a", "b"},
		},
		{
			name: "包含范围边界",
			groups: []MatchConditions{
				{NumberRanges: map[string]NumberRange{
					"score": {Min: 1500, Max: 1800},
				}},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "组内AND",
			groups: []MatchConditions{{
				StringEquals: map[string]string{"region": "asia"},
				NumberRanges: map[string]NumberRange{
					"score": {Min: 1800, Max: 1800},
				},
			}},
		},
		{
			name: "组间OR和去重",
			groups: []MatchConditions{
				{StringEquals: map[string]string{"region": "asia"}},
				{NumberRanges: map[string]NumberRange{
					"score": {Min: 1500, Max: 1800},
				}},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "缺少属性",
			groups: []MatchConditions{
				{NumberRanges: map[string]NumberRange{
					"level": {Min: 0, Max: 10},
				}},
			},
		},
		{
			name: "失败的Add没有残留索引",
			groups: []MatchConditions{
				{StringEquals: map[string]string{"region": "invalid"}},
			},
		},
		{
			name:   "空条件组",
			groups: []MatchConditions{{}},
			want:   []string{"a", "b", "c", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := MatchStrategy{AnyOf: tt.groups}
			seen := make(map[string]bool)
			var got []string

			for _, id := range index.Search(strategy, 100) {
				if seen[id] {
					t.Fatalf("重复返回候选 %q", id)
				}
				seen[id] = true

				// Search 是粗筛，还需要完整条件判定。
				if strategy.Accept(tickets[id]) {
					got = append(got, id)
				}
			}

			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("得到 %v，期望 %v", got, tt.want)
			}
		})
	}

	rangeQuery := MatchStrategy{
		AnyOf: []MatchConditions{{
			NumberRanges: map[string]NumberRange{
				"score": {Min: 1500, Max: 1500},
			},
		}},
	}

	// 删除一张票，不应影响同分的另一张。
	index.Remove(tickets["a"])
	if got := index.Search(rangeQuery, 100); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("删除 a 后，同分票 b 的索引异常：%v", got)
	}
	if _, exists := index.indexToId["leader-a"]; exists {
		t.Fatal("业务标识没有删除")
	}

	for _, id := range []string{"b", "c", "d"} {
		index.Remove(tickets[id])
	}
	if len(index.indexToId) != 0 ||
		len(index.stringIndex) != 0 ||
		len(index.numberIndex) != 0 {
		t.Fatal("删除全部票后，仍有索引残留")
	}

	// 删除后可以重新加入。
	if err := index.Add(tickets["a"]); err != nil {
		t.Fatal(err)
	}
	if got := index.Search(rangeQuery, 100); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("重新加入后查询异常：%v", got)
	}

	if got := index.Search(MatchStrategy{}, 0); len(got) != 0 {
		t.Fatalf("扫描预算为零，却返回了 %v", got)
	}

	if err := index.Add(tickets["c"]); err != nil {
		t.Fatal(err)
	}

	// 前两个分支都扫描 a，预算为 2 时不能继续扫描 c。
	repeated := MatchStrategy{
		AnyOf: []MatchConditions{
			{StringEquals: map[string]string{"region": "asia"}},
			{NumberRanges: map[string]NumberRange{
				"score": {Min: 1500, Max: 1500},
			}},
			{StringEquals: map[string]string{"region": "europe"}},
		},
	}
	if got := index.Search(repeated, 2); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("重复扫描未正确计入预算：%v", got)
	}
}

func TestMatchIndexPlayerConflict(t *testing.T) {
	for _, tc := range []struct {
		name  string
		index *MatchIndex[int]
	}{
		{"zero value", &MatchIndex[int]{}},
		{"constructor", NewMatchIndex[int]()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := tc.index
			a := &Ticket[int]{
				Id: "a", Index: "leader-a",
				Players: map[int]*Player[int]{1: {Uid: 1, TicketId: "a"}},
			}
			b := &Ticket[int]{
				Id: "b", Index: "leader-b",
				Players: map[int]*Player[int]{
					1: {Uid: 1, TicketId: "b"},
					2: {Uid: 2, TicketId: "b"},
				},
				StringData: map[string]string{"region": "asia"},
				NumberData: map[string]float64{"score": 1500},
			}
			if err := index.Add(a); err != nil {
				t.Fatal(err)
			}
			if err := index.Add(b); !errors.Is(err, ErrPlayerDuplicated) {
				t.Fatalf("玩家重复：得到 %v，期望 ErrPlayerDuplicated", err)
			}
			if index.playerToTicketId[1] != "a" {
				t.Fatal("失败的 Add 覆盖了玩家所属票")
			}
			if _, exists := index.playerToTicketId[2]; exists {
				t.Fatal("失败的 Add 占用了未冲突的玩家")
			}
			if _, exists := index.indexToId[b.Index]; exists || len(index.stringIndex) != 0 || len(index.numberIndex) != 0 {
				t.Fatal("失败的 Add 留下了索引")
			}

			index.Remove(a)
			if err := index.Add(b); err != nil {
				t.Fatalf("移除原票后玩家应可重新入队：%v", err)
			}
			for uid := range b.Players {
				if index.playerToTicketId[uid] != b.Id {
					t.Fatalf("玩家 %d 未关联新票", uid)
				}
			}
			index.Remove(b)
			if len(index.playerToTicketId) != 0 || len(index.indexToId) != 0 || len(index.stringIndex) != 0 || len(index.numberIndex) != 0 {
				t.Fatal("移除后仍有索引残留")
			}
		})
	}
}

func BenchmarkMatchIndex(b *testing.B) {
	const batch = 256

	for _, size := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			index := NewMatchIndex[string]()
			tickets := make([]*Ticket[string], size+batch)
			regions := []string{"asia", "europe", "america", "africa"}

			for i := range tickets {
				id := strconv.Itoa(i)
				tickets[i] = &Ticket[string]{
					Id:    id,
					Index: id,
					Players: map[string]*Player[string]{
						id: {Uid: id, TicketId: id},
					},
					StringData: map[string]string{
						"region": regions[i%len(regions)],
					},
					NumberData: map[string]float64{
						"score": float64(i % 10000),
					},
				}

				if i < size {
					if err := index.Add(tickets[i]); err != nil {
						b.Fatal(err)
					}
				}
			}

			pending := tickets[size:]

			b.Run("Add", func(b *testing.B) {
				b.StopTimer()
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					b.StartTimer()
					for _, ticket := range pending {
						if err := index.Add(ticket); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()

					// 清理不计入 Add 耗时。
					for _, ticket := range pending {
						index.Remove(ticket)
					}
				}

				b.ReportMetric(
					float64(b.Elapsed().Nanoseconds())/float64(b.N*batch),
					"ns/ticket",
				)
			})

			b.Run("Remove", func(b *testing.B) {
				b.StopTimer()
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					// 准备数据不计入 Remove 耗时。
					for _, ticket := range pending {
						if err := index.Add(ticket); err != nil {
							b.Fatal(err)
						}
					}

					b.StartTimer()
					for _, ticket := range pending {
						index.Remove(ticket)
					}
					b.StopTimer()
				}

				b.ReportMetric(
					float64(b.Elapsed().Nanoseconds())/float64(b.N*batch),
					"ns/ticket",
				)
			})

			queries := []struct {
				name     string
				strategy MatchStrategy
			}{
				{
					name: "String",
					strategy: MatchStrategy{
						AnyOf: []MatchConditions{{
							StringEquals: map[string]string{"region": "asia"},
						}},
					},
				},
				{
					name: "Number",
					strategy: MatchStrategy{
						AnyOf: []MatchConditions{{
							NumberRanges: map[string]NumberRange{
								"score": {Min: 400, Max: 600},
							},
						}},
					},
				},
			}

			for _, query := range queries {
				b.Run("Search/"+query.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()

					for i := 0; i < b.N; i++ {
						got := index.Search(query.strategy, 100)
						if len(got) != 100 {
							b.Fatalf("得到 %d 个候选，期望 100", len(got))
						}
					}
				})
			}
		})
	}
}
