package ai

import (
	"errors"
	"fmt"
	"sync"
)

// AssistantStream manages a stream of assistant message events, providing a
// thread-safe push-based iterator that is upstream-compatible.
type AssistantStream struct {
	mu          sync.Mutex
	queue       []AssistantMessageEvent
	queueLimit  int
	eventsChan  chan AssistantMessageEvent
	cond        *sync.Cond
	pushClosed  bool
	resultMsg   AssistantMessage
	resultErr   error
	resultReady chan struct{}
	startDrain  sync.Once
}

// NewAssistantStream creates a new AssistantStream with a bounded queue limit.
// If limit is less than or equal to 0, a default safety limit of 1000 is used.
func NewAssistantStream(limit int) *AssistantStream {
	if limit <= 0 {
		limit = 1000
	}
	s := &AssistantStream{
		queueLimit:  limit,
		eventsChan:  make(chan AssistantMessageEvent),
		resultReady: make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)

	return s
}

// drainLoop runs in a background goroutine, reading from the queue and writing
// to the consumer channel (eventsChan). It shuts down and closes eventsChan
// when pushClosed is true and the queue is completely drained.
func (s *AssistantStream) drainLoop() {
	defer func() {
		s.mu.Lock()
		close(s.eventsChan)
		s.mu.Unlock()
	}()

	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.pushClosed {
			s.cond.Wait()
		}

		if len(s.queue) == 0 && s.pushClosed {
			s.mu.Unlock()
			return
		}

		event := s.queue[0]
		// Clear reference to avoid potential memory retention
		s.queue[0] = AssistantMessageEvent{}
		s.queue = s.queue[1:]
		s.mu.Unlock()

		s.eventsChan <- event
	}
}

// validateStopReason checks that StopReason matches the allowed partition.
// done events can only carry "stop", "length", or "toolUse".
// error events can only carry "error" or "aborted".
func validateStopReason(eventType AssistantMessageEventType, reason StopReason) error {
	if eventType == EventDone {
		switch reason {
		case StopReasonStop, StopReasonLength, StopReasonToolUse:
			return nil
		default:
			return fmt.Errorf("invalid StopReason %q for EventDone", reason)
		}
	}
	if eventType == EventError {
		switch reason {
		case StopReasonError, StopReasonAborted:
			return nil
		default:
			return fmt.Errorf("invalid StopReason %q for EventError", reason)
		}
	}
	return nil
}

// Push enqueues a stream event. If the queue reaches its safety bound,
// Push returns a queue-overflow error. After done or error, further pushes
// are rejected as no-ops.
func (s *AssistantStream) Push(event AssistantMessageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pushClosed {
		return nil
	}
	if err := validateStopReason(event.Type, event.Reason); err != nil {
		return err
	}
	isTerminal := event.Type == EventDone || event.Type == EventError
	if len(s.queue) >= s.queueLimit && !isTerminal {
		return fmt.Errorf("queue overflow: stream queue limit of %d reached", s.queueLimit)
	}

	s.enqueueLocked(event, nil)
	return nil
}

// enqueueLocked appends an event to the queue and signals the drain loop.
// If the event terminates the stream, pushClosed is marked true and the
// stream result is resolved.
func (s *AssistantStream) enqueueLocked(event AssistantMessageEvent, optErr error) {
	// Deep copy the event payload to prevent snapshot aliasing
	event = deepCopyEvent(event)

	s.queue = append(s.queue, event)
	s.cond.Signal()

	if event.Type == EventDone || event.Type == EventError {
		s.pushClosed = true
		s.resolveResultLocked(event, optErr)
	}
}

// resolveResultLocked populates the final stream message and error, and closes
// the resultReady channel to unblock any waiters on Result().
func (s *AssistantStream) resolveResultLocked(event AssistantMessageEvent, optErr error) {
	select {
	case <-s.resultReady:
		return
	default:
	}

	if event.Type == EventDone {
		var msg AssistantMessage
		if event.Message != nil {
			msg = *event.Message
		} else if event.Partial != nil {
			msg = *event.Partial
		}
		s.resultMsg = msg
		s.resultErr = nil
	} else if event.Type == EventError {
		var msg AssistantMessage
		if event.Error != nil {
			msg = *event.Error
		} else if event.Partial != nil {
			msg = *event.Partial
		}
		s.resultMsg = msg

		if optErr != nil {
			s.resultErr = optErr
		} else if msg.ErrorMessage != "" {
			s.resultErr = errors.New(msg.ErrorMessage)
		} else if event.Reason != "" {
			s.resultErr = fmt.Errorf("stream error with stop reason: %s", event.Reason)
		} else {
			s.resultErr = errors.New("stream error")
		}
	}
	close(s.resultReady)
}

// End performs an explicit successful termination, resolving Result() with the
// final message and nil error. It bypasses queue limits for safe shutdown.
func (s *AssistantStream) End(result *AssistantMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pushClosed {
		return
	}

	var msg AssistantMessage
	if result != nil {
		msg = *result
	}

	reason := msg.StopReason
	// Validate/canonicalize stop reason for done
	switch reason {
	case StopReasonStop, StopReasonLength, StopReasonToolUse:
		// valid
	default:
		reason = StopReasonStop
	}
	msg.StopReason = reason

	event := AssistantMessageEvent{
		Type:    EventDone,
		Message: &msg,
		Reason:  reason,
	}

	s.enqueueLocked(event, nil)
}

// Error performs an explicit failed termination, resolving Result() with the
// provided partial message and non-nil error. It bypasses queue limits.
func (s *AssistantStream) Error(err error, partial *AssistantMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pushClosed {
		return
	}

	var msg AssistantMessage
	if partial != nil {
		msg = *partial
	}

	if err == nil {
		err = errors.New("stream error")
	}

	if msg.ErrorMessage == "" {
		msg.ErrorMessage = err.Error()
	}

	reason := msg.StopReason
	// Validate/canonicalize stop reason for error
	switch reason {
	case StopReasonError, StopReasonAborted:
		// valid
	default:
		reason = StopReasonError
	}
	msg.StopReason = reason

	event := AssistantMessageEvent{
		Type:    EventError,
		Partial: &msg,
		Error:   &msg,
		Reason:  reason,
	}

	s.enqueueLocked(event, err)
}

// Events returns a read-only channel for consuming stream events.
// It lazy-starts the background drain loop on first call.
func (s *AssistantStream) Events() <-chan AssistantMessageEvent {
	s.startDrain.Do(func() {
		go s.drainLoop()
	})
	return s.eventsChan
}

// Result blocks until the stream completes, then returns the final message and
// nil error on success, or the partial message and non-nil error on failure.
func (s *AssistantStream) Result() (AssistantMessage, error) {
	<-s.resultReady
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resultMsg, s.resultErr
}

// deepCopyEvent creates a deep copy of the event fields to prevent snapshot aliasing.
func deepCopyEvent(event AssistantMessageEvent) AssistantMessageEvent {
	copied := event
	if event.ToolCall != nil {
		copied.ToolCall = deepCopyToolCall(event.ToolCall)
	}
	if event.Partial != nil {
		copied.Partial = deepCopyAssistantMessage(event.Partial)
	}
	if event.Message != nil {
		copied.Message = deepCopyAssistantMessage(event.Message)
	}
	if event.Error != nil {
		copied.Error = deepCopyAssistantMessage(event.Error)
	}
	return copied
}

func deepCopyToolCall(t *ToolCall) *ToolCall {
	if t == nil {
		return nil
	}
	return &ToolCall{
		ID:               t.ID,
		Name:             t.Name,
		Arguments:        deepCopyMap(t.Arguments),
		ThoughtSignature: t.ThoughtSignature,
	}
}

func deepCopyAssistantMessage(m *AssistantMessage) *AssistantMessage {
	if m == nil {
		return nil
	}
	if cp, ok := m.DeepCopy().(*AssistantMessage); ok {
		return cp
	}
	return m
}
