package ws

import (
	"net/http"
	"strconv"
	"sync"

	"ai-auto-annotator/backend/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Hub broadcasts job progress events to every connected websocket client.
// Each connection gets its own buffered channel; a previous implementation
// used one shared channel, which made concurrent clients steal each other's
// events instead of all receiving them.
type Hub struct {
	mu   sync.Mutex
	subs map[chan model.Job]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan model.Job]struct{})}
}

func (h *Hub) Publish(job model.Job) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- job:
		default: // slow client: drop instead of blocking job execution
		}
	}
}

func (h *Hub) subscribe() chan model.Job {
	ch := make(chan model.Job, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan model.Job) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) Handle(c *gin.Context) {
	jobID, _ := strconv.ParseInt(c.Query("job_id"), 10, 64)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := h.subscribe()
	defer h.unsubscribe(ch)

	// Reader goroutine: surfaces client disconnects (and consumes pings).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case job := <-ch:
			if jobID != 0 && job.ID != jobID {
				continue
			}
			if err := conn.WriteJSON(map[string]any{
				"type":      "job_progress",
				"job_id":    job.ID,
				"job_type":  job.Type,
				"status":    job.Status,
				"processed": job.Processed,
				"total":     job.Total,
				"error":     job.Error,
			}); err != nil {
				return
			}
		}
	}
}
