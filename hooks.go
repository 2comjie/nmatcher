package nmatcher

// Pass 在提交匹配结果之前执行，返回 false 表示拒绝当前组合
type Pass[T PlayerId] func(result MatchResult[T]) bool

// OnMatched 在匹配成功、参与票移出队列之后执行
type OnMatched[T PlayerId] func(result MatchResult[T])

// OnTimeout 在超时票移出队列之后执行
type OnTimeout[T PlayerId] func(ticket Ticket[T])
