package ai

import (
	"context"
	"fmt"
)

// Stream looks up the provider via GetApiProvider(model.API). If missing, it returns
// a pre-completed AssistantStream immediately carrying an error event with StopReason = "error".
func Stream(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
	p, ok := GetApiProvider(model.API)
	if !ok {
		return newErrorStream(fmt.Errorf("API provider %q not found", model.API))
	}
	return p.Stream(ctx, model, c, opts)
}

// Complete calls Stream and blocks on Result().
func Complete(ctx context.Context, model Model, c Context, opts *StreamOptions) (AssistantMessage, error) {
	s := Stream(ctx, model, c, opts)
	return s.Result()
}

// StreamSimple looks up the provider via GetApiProvider(model.API). If missing, it returns
// a pre-completed AssistantStream immediately carrying an error event with StopReason = "error".
func StreamSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
	p, ok := GetApiProvider(model.API)
	if !ok {
		return newErrorStream(fmt.Errorf("API provider %q not found", model.API))
	}
	return p.StreamSimple(ctx, model, c, opts)
}

// CompleteSimple calls StreamSimple and blocks on Result().
func CompleteSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) (AssistantMessage, error) {
	s := StreamSimple(ctx, model, c, opts)
	return s.Result()
}
