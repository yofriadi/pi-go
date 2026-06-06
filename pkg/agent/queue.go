package agent

import (
	"sync"
)

// DrainMode controls how messages are pulled from the queue per turn.
type DrainMode string

const (
	// DrainModeAll drains all messages in the queue at once.
	DrainModeAll DrainMode = "all"

	// DrainModeOneAtATime drains exactly one message from the queue per turn.
	DrainModeOneAtATime DrainMode = "one-at-a-time"
)

// MessageQueue is a thread-safe FIFO queue of AgentMessages with a configurable drain mode.
type MessageQueue struct {
	mu        sync.Mutex
	messages  []AgentMessage
	drainMode DrainMode
}

// NewMessageQueue creates a new MessageQueue with the given DrainMode.
func NewMessageQueue(mode DrainMode) *MessageQueue {
	return &MessageQueue{
		drainMode: mode,
	}
}

// Push appends a message to the end of the queue.
func (q *MessageQueue) Push(msg AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msg)
}

// PushMany appends multiple messages to the end of the queue.
func (q *MessageQueue) PushMany(msgs []AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msgs...)
}

// Drain pulls messages from the queue according to the DrainMode.
func (q *MessageQueue) Drain() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	if q.drainMode == DrainModeOneAtATime {
		msg := q.messages[0]
		q.messages = q.messages[1:]
		return []AgentMessage{msg}
	}
	// Default to DrainModeAll
	msgs := q.messages
	q.messages = nil
	return msgs
}

// Len returns the current number of messages in the queue.
func (q *MessageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// SetDrainMode updates the queue's drain mode.
func (q *MessageQueue) SetDrainMode(mode DrainMode) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.drainMode = mode
}
