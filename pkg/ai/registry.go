package ai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// StreamFunc defines the signature for streaming with complete options.
type StreamFunc func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream

// StreamSimpleFunc defines the signature for streaming with simplified options.
type StreamSimpleFunc func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream

// ApiProvider defines a registered API adapter's streaming functions.
type ApiProvider struct {
	API          APIID
	Stream       StreamFunc
	StreamSimple StreamSimpleFunc
}

var (
	providersMu sync.RWMutex
	providers   = make(map[APIID]ApiProvider)
)

// RegisterApiProvider registers an ApiProvider. It returns an error if the input validation fails.
// It wraps the provider's functions with an API-mismatch guard.
func RegisterApiProvider(p ApiProvider) error {
	if p.API == "" {
		return errors.New("cannot register ApiProvider: API ID cannot be empty")
	}
	if p.Stream == nil {
		return errors.New("cannot register ApiProvider: Stream function cannot be nil")
	}
	if p.StreamSimple == nil {
		return errors.New("cannot register ApiProvider: StreamSimple function cannot be nil")
	}

	wrappedStream := func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
		if model.API != p.API {
			return newErrorStream(fmt.Errorf("API mismatch: model API %q does not match registered provider API %q", model.API, p.API))
		}
		return p.Stream(ctx, model, c, opts)
	}

	wrappedStreamSimple := func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
		if model.API != p.API {
			return newErrorStream(fmt.Errorf("API mismatch: model API %q does not match registered provider API %q", model.API, p.API))
		}
		return p.StreamSimple(ctx, model, c, opts)
	}

	providersMu.Lock()
	defer providersMu.Unlock()

	if _, exists := providers[p.API]; exists {
		return fmt.Errorf("cannot register ApiProvider: API %q is already registered", p.API)
	}

	providers[p.API] = ApiProvider{
		API:          p.API,
		Stream:       wrappedStream,
		StreamSimple: wrappedStreamSimple,
	}

	return nil
}

func newErrorStream(err error) *AssistantStream {
	s := NewAssistantStream(1)
	s.Error(err, nil)
	return s
}

// GetApiProvider retrieves a registered ApiProvider by APIID.
func GetApiProvider(api APIID) (ApiProvider, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	p, ok := providers[api]
	return p, ok
}

// GetApiProviders returns all registered ApiProviders sorted alphabetically by APIID.
func GetApiProviders() []ApiProvider {
	providersMu.RLock()
	defer providersMu.RUnlock()

	keys := make([]APIID, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	result := make([]ApiProvider, 0, len(keys))
	for _, k := range keys {
		result = append(result, providers[k])
	}
	return result
}

// ClearApiProviders empties the registry map.
func ClearApiProviders() {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers = make(map[APIID]ApiProvider)
}
