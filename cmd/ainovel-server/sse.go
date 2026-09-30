package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Message 是 SSE 通道上的一条消息。
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Hub 是单主题的 SSE 广播器。订阅者各有独立缓冲，满了丢最旧的——
// 慢浏览器不能反压到 Host 的事件通道（那条通道本身是"丢最旧"语义，
// 堵住会直接丢掉引擎事件）。
type Hub struct {
	mu   sync.RWMutex
	subs map[int]chan Message
	next int
	seq  int64
}

// subscriberBuffer 是单个订阅者的缓冲深度。
const subscriberBuffer = 512

func NewHub() *Hub {
	return &Hub{subs: map[int]chan Message{}}
}

// Publish 广播一条消息。永不阻塞。
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	h.seq++
	if h.seq%64 == 0 {
		// 周期性心跳注释帧，兼顾代理超时与连接保活。
		for _, ch := range h.subs {
			select {
			case ch <- Message{Type: "ping"}:
			default:
			}
		}
	}
	for _, ch := range h.subs {
		select {
		case ch <- m:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- m:
			default:
			}
		}
	}
	h.mu.Unlock()
}

// Seq 返回当前序号，供客户端在 Last-Event-ID 之后续传。
func (h *Hub) Seq() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.seq
}

func (h *Hub) Subscribe() (int, <-chan Message) {
	ch := make(chan Message, subscriberBuffer)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	return id, ch
}

func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

// Count 返回当前订阅者数量。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// serveSSE 把 Hub 接到一个 HTTP 长连接上。断开时自动退订。
func serveSSE(w http.ResponseWriter, r *http.Request, hub *Hub, initial func() []Message) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("当前连接不支持流式响应"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 先冲一次，浏览器能立刻收到响应头而不是等首个事件。
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	id, ch := hub.Subscribe()
	defer hub.Unsubscribe(id)

	// 订阅瞬间的快照先补一遍，客户端不必等下一个状态变化。
	for _, m := range initial() {
		writeSSE(w, m)
	}
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			if m.Type == "ping" {
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				continue
			}
			writeSSE(w, m)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, m Message) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Type, data)
}
