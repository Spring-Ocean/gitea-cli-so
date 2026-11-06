// Package compat 集中维护版本声明与静态定义证据。
// 版本检测不会证明业务兼容，也不通过有副作用请求探测能力。
package compat

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Info 保存原始版本及彼此独立的检测、定义和实例证据。
// ExactMatch 仅对七个精确正式字符串成立，带后缀版本不得冒充正式版。
type Info struct {
	URL                string     `json:"url"`
	Raw                string     `json:"raw_version,omitempty"`
	Major              int        `json:"major,omitempty"`
	Minor              int        `json:"minor,omitempty"`
	Patch              int        `json:"patch,omitempty"`
	Detected           string     `json:"detection"`
	ExactMatch         bool       `json:"exact_match"`
	DefinitionEvidence string     `json:"definition_evidence"`
	InstanceEvidence   string     `json:"instance_evidence"`
	FetchedAt          time.Time  `json:"fetched_at"`
	Cached             bool       `json:"cached"`
	Error              *app.Error `json:"error,omitempty"`
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[-+][0-9A-Za-z.-]+)?$`)

// Parse 保留版本原文并在格式明确时解析数字，不限制主版本为 1。
// 无法解析时保持 unknown；静态定义命中不改变实例未测试状态。
func Parse(raw string) Info {
	info := Info{
		Raw:                raw,
		Detected:           "unknown",
		DefinitionEvidence: "not_researched",
		InstanceEvidence:   "not_tested",
	}
	match := versionPattern.FindStringSubmatch(raw)
	if match == nil {
		return info
	}
	values := []int{0, 0, 0}
	for i, text := range match[1:] {
		value, err := strconv.Atoi(text)
		if err != nil {
			return info
		}
		values[i] = value
	}
	info.Major, info.Minor, info.Patch = values[0], values[1], values[2]
	info.Detected = "identified"
	switch raw {
	case "1.22.6", "1.23.8", "1.24.7", "1.25.5", "1.26.4", "1.27.3", "28.3.0":
		info.ExactMatch = true
		info.DefinitionEvidence = "analyzed"
	}
	return info
}

// Detect 在有限预算内读取 /version，不读取其他实例或账号。
// 无效 JSON、权限和网络错误分别记录，失败绝不推断平台不是 Gitea。
func Detect(ctx context.Context, client *gitea.Client, root string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info := Parse("")
	info.URL, info.FetchedAt = root, time.Now().UTC()
	response, err := client.Do(ctx, gitea.Request{Path: "/version"})
	if err == nil {
		var payload struct {
			Version string `json:"version"`
		}
		err = gitea.Decode(response, &payload)
		if err == nil && len(payload.Version) > 256 {
			err = app.Wrap(app.VersionUnknown, "The declared version exceeds the supported diagnostic length.", nil)
		}
		if err == nil {
			parsed := Parse(payload.Version)
			parsed.URL, parsed.FetchedAt = info.URL, info.FetchedAt
			info = parsed
			if info.Detected == "unknown" {
				err = app.Wrap(app.VersionUnknown, "The server version is unknown. Check the root URL, proxy, or server response.", nil)
			}
		}
	}
	if err != nil {
		info.Error = app.Failure(err)
	}
	return info, err
}

// Cache 保存进程内检测结果及在途请求，不持久化密钥或响应原文。
// key 由调用方纳入完整实例、账号、配置及凭证变更标识，TTL 可在构造时调整。
type Cache struct {
	mu         sync.Mutex
	entries    map[string]entry
	flights    map[string]chan struct{}
	SuccessTTL time.Duration
	FailureTTL time.Duration
	Now        func() time.Time
}

type entry struct {
	info    Info
	err     error
	expires time.Time
}

// Get 合并同一 key 的并发读取，等待者可独立取消。
// refresh 忽略已有结果；在途请求仍共享，首个调用者取消会终止该轮检测。
func (c *Cache) Get(ctx context.Context, key string, refresh bool, fetch func(context.Context) (Info, error)) (Info, error) {
	for {
		c.mu.Lock()
		if c.entries == nil {
			c.entries = map[string]entry{}
			c.flights = map[string]chan struct{}{}
		}
		now := time.Now()
		if c.Now != nil {
			now = c.Now()
		}
		if value, ok := c.entries[key]; ok && !refresh && now.Before(value.expires) {
			info := value.info
			info.Cached = true
			c.mu.Unlock()
			return info, value.err
		}
		if flight := c.flights[key]; flight != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return Info{}, app.Failure(ctx.Err())
			case <-flight:
				refresh = false
				continue
			}
		}
		flight := make(chan struct{})
		c.flights[key] = flight
		c.mu.Unlock()
		info, err := fetch(ctx)
		ttl := c.SuccessTTL
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		if err != nil {
			ttl = c.FailureTTL
			if ttl <= 0 {
				ttl = 30 * time.Second
			}
		}
		c.mu.Lock()
		if c.Now != nil {
			now = c.Now()
		} else {
			now = time.Now()
		}
		// 只保留有界条目，配置修订变更后不会无界积累历史身份。
		if len(c.entries) >= 128 {
			c.entries = map[string]entry{}
		}
		if ctx.Err() == nil {
			c.entries[key] = entry{
				info:    info,
				err:     err,
				expires: now.Add(ttl),
			}
		}
		delete(c.flights, key)
		close(flight)
		c.mu.Unlock()
		return info, err
	}
}
