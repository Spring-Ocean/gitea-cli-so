// Package write 提供多业务共享的写入并发边界。
// 进程内串行不等于服务器事务，也不阻止其他用户或进程的并发修改。
package write

import (
	"context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"sync"
)

// Locks 按完整实例、账号和仓库键串行同进程写入。
// 零值可用；只有真实等待者持有条目，释放后移除，避免无限增长。
type Locks struct {
	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	gate       chan struct{}
	references int
}

// Acquire 等待目标写入锁，返回必须调用一次的释放函数。
// 等待支持 context 取消；调用者仍需在写请求前检查取消和远端前置条件。
func (locks *Locks) Acquire(ctx context.Context, key string) (func(), error) {
	locks.mu.Lock()
	if locks.entries == nil {
		locks.entries = map[string]*entry{}
	}
	current := locks.entries[key]
	if current == nil {
		current = &entry{gate: make(chan struct{}, 1)}
		locks.entries[key] = current
	}
	current.references++
	locks.mu.Unlock()
	select {
	case current.gate <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-current.gate
				locks.release(key, current)
			})
		}, nil
	case <-ctx.Done():
		locks.release(key, current)
		return nil, app.Failure(ctx.Err())
	}
}

// release 在等待者或持有者离开时回收空闲键。
// 当前条目仍有引用时不删除，保证新等待者不会绕过已建立的串行顺序。
func (locks *Locks) release(key string, current *entry) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	current.references--
	if current.references == 0 {
		delete(locks.entries, key)
	}
}
