package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"pi-go/pkg/ai"
)

func TestParseFlags_Basic(t *testing.T) {
	args := []string{"-p", "hello world", "--provider", "openai-codex", "--model", "gpt-5.4-mini", "--thinking", "low"}
	opts, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("unexpected error parsing flags: %v", err)
	}

	if opts.PrintPrompt != "hello world" {
		t.Errorf("expected print prompt 'hello world', got %q", opts.PrintPrompt)
	}
	if opts.Provider != "openai-codex" {
		t.Errorf("expected provider 'openai-codex', got %q", opts.Provider)
	}
	if opts.ModelID != "gpt-5.4-mini" {
		t.Errorf("expected model 'gpt-5.4-mini', got %q", opts.ModelID)
	}
	if opts.Thinking != "low" {
		t.Errorf("expected thinking 'low', got %q", opts.Thinking)
	}
}

func TestParseFlags_Aliases(t *testing.T) {
	args := []string{"--print", "hello print alias"}
	opts, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.PrintPrompt != "hello print alias" {
		t.Errorf("expected print prompt 'hello print alias', got %q", opts.PrintPrompt)
	}
}

func TestParseFlags_ForbiddenFlags(t *testing.T) {
	forbiddenArgs := [][]string{
		{"--api-key", "my-key"},
		{"-api-key", "my-key"},
		{"--max-turns", "5"},
		{"-max-turns", "5"},
	}

	for _, args := range forbiddenArgs {
		_, err := ParseFlags(args)
		if err == nil {
			t.Errorf("expected error for forbidden flags: %v", args)
		} else if !strings.Contains(err.Error(), "forbidden") && !strings.Contains(err.Error(), "not supported") {
			t.Errorf("unexpected error message for %v: %v", args, err)
		}
	}
}

func TestParseFlags_ProviderRejection(t *testing.T) {
	args := []string{"--provider", "anthropic"}
	_, err := ParseFlags(args)
	if err == nil {
		t.Error("expected error for unsupported provider, got nil")
	} else if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseFlags_ModelValidation(t *testing.T) {
	t.Run("Valid model", func(t *testing.T) {
		opts, err := ParseFlags([]string{"--model", "gpt-5.5"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.ModelID != "gpt-5.5" {
			t.Errorf("expected model 'gpt-5.5', got %q", opts.ModelID)
		}
	})

	t.Run("Invalid model", func(t *testing.T) {
		_, err := ParseFlags([]string{"--model", "invalid-model"})
		if err == nil {
			t.Error("expected error for invalid model, got nil")
		} else if !strings.Contains(err.Error(), "not found in registry") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestParseFlags_ThinkingValidation(t *testing.T) {
	t.Run("Valid thinking levels", func(t *testing.T) {
		levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
		for _, l := range levels {
			opts, err := ParseFlags([]string{"--thinking", l})
			if err != nil {
				t.Fatalf("unexpected error for thinking level %q: %v", l, err)
			}
			if opts.Thinking != l {
				t.Errorf("expected thinking level %q, got %q", l, opts.Thinking)
			}
		}
	})

	t.Run("Invalid thinking level", func(t *testing.T) {
		_, err := ParseFlags([]string{"--thinking", "invalid"})
		if err == nil {
			t.Error("expected error for invalid thinking level, got nil")
		} else if !strings.Contains(err.Error(), "invalid thinking level") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestBuildToolRegistry(t *testing.T) {
	t.Run("Default built-ins", func(t *testing.T) {
		opts := &CLIOptions{}
		registry := buildToolRegistry(opts)
		defs := registry.Definitions()
		var names []string
		for _, d := range defs {
			names = append(names, d.Name)
		}
		slices.Sort(names)
		expected := []string{"bash", "edit", "read", "write"}
		if !slices.Equal(names, expected) {
			t.Errorf("expected tools %v, got %v", expected, names)
		}
	})

	t.Run("No tools flag", func(t *testing.T) {
		opts := &CLIOptions{NoTools: true}
		registry := buildToolRegistry(opts)
		if len(registry.Definitions()) != 0 {
			t.Errorf("expected 0 tools, got %d", len(registry.Definitions()))
		}
	})

	t.Run("No builtin tools flag", func(t *testing.T) {
		opts := &CLIOptions{NoBuiltinTools: true}
		registry := buildToolRegistry(opts)
		if len(registry.Definitions()) != 0 {
			t.Errorf("expected 0 tools, got %d", len(registry.Definitions()))
		}
	})

	t.Run("Explicit tools list", func(t *testing.T) {
		opts := &CLIOptions{Tools: "read,bash"}
		registry := buildToolRegistry(opts)
		defs := registry.Definitions()
		var names []string
		for _, d := range defs {
			names = append(names, d.Name)
		}
		slices.Sort(names)
		expected := []string{"bash", "read"}
		if !slices.Equal(names, expected) {
			t.Errorf("expected tools %v, got %v", expected, names)
		}
	})

	t.Run("Exclude tools list", func(t *testing.T) {
		opts := &CLIOptions{ExcludeTools: "write,edit"}
		registry := buildToolRegistry(opts)
		defs := registry.Definitions()
		var names []string
		for _, d := range defs {
			names = append(names, d.Name)
		}
		slices.Sort(names)
		expected := []string{"bash", "read"}
		if !slices.Equal(names, expected) {
			t.Errorf("expected tools %v, got %v", expected, names)
		}
	})
}

func TestPositionalArgsAsPrompt(t *testing.T) {
	args := []string{"what", "is", "pi?"}
	opts, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.PrintPrompt != "what is pi?" {
		t.Errorf("expected positional prompt 'what is pi?', got %q", opts.PrintPrompt)
	}
}

func TestParseFlags_ModelNormalization(t *testing.T) {
	t.Run("Valid provider prefix", func(t *testing.T) {
		opts, err := ParseFlags([]string{"--model", "openai-codex/gpt-5.4"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.ModelID != "gpt-5.4" {
			t.Errorf("expected normalized model ID 'gpt-5.4', got %q", opts.ModelID)
		}
	})
	t.Run("Invalid provider prefix", func(t *testing.T) {
		_, err := ParseFlags([]string{"--model", "unsupported-provider/gpt-5.4"})
		if err == nil {
			t.Error("expected error for unsupported provider prefix, got nil")
		} else if !strings.Contains(err.Error(), "unsupported provider prefix") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestParseFlags_HelpErrHelp(t *testing.T) {
	_, err := ParseFlags([]string{"--help"})
	if err == nil {
		t.Fatal("expected flag.ErrHelp error, got nil")
	}
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("expected flag.ErrHelp error, got %v", err)
	}
}

func TestParseFlags_ThinkingClamping(t *testing.T) {
	// Since all registered models currently support reasoning and all valid thinking levels,
	// ClampThinkingLevel resolves to the input level unchanged. We assert that
	// a valid level is correctly parsed and passed through.
	opts, err := ParseFlags([]string{"--model", "gpt-5.4-mini", "--thinking", "minimal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Thinking != "minimal" {
		t.Errorf("expected thinking 'minimal', got %q", opts.Thinking)
	}
}

func TestCLI_ClampThinkingLevelSynthetic(t *testing.T) {
	t.Run("Non-reasoning model clamps to off", func(t *testing.T) {
		m := ai.Model{
			Reasoning: false,
		}
		clamped := ai.ClampThinkingLevel(m, ai.ModelThinkingLevelLow)
		if clamped != ai.ModelThinkingLevelOff {
			t.Errorf("expected low thinking level to clamp to off for non-reasoning model, got %q", clamped)
		}
	})
	t.Run("Reasoning model with limited support clamps to closest", func(t *testing.T) {
		strVal := "low"
		m := ai.Model{
			Reasoning: true,
			ThinkingLevelMap: map[ai.ModelThinkingLevel]*string{
				ai.ModelThinkingLevelMinimal: &strVal,
				ai.ModelThinkingLevelLow:     nil,
				ai.ModelThinkingLevelMedium:  nil,
				ai.ModelThinkingLevelHigh:    nil,
				ai.ModelThinkingLevelXHigh:   nil,
			},
		}
		// Clamping xhigh should search down and find minimal as the closest supported level.
		clamped := ai.ClampThinkingLevel(m, ai.ModelThinkingLevelXHigh)
		if clamped != ai.ModelThinkingLevelMinimal {
			t.Errorf("expected xhigh to clamp to minimal, got %q", clamped)
		}
	})
}

func TestParseFlags_Mode(t *testing.T) {
	t.Run("Valid mode text", func(t *testing.T) {
		opts, err := ParseFlags([]string{"--mode", "text"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Mode != "text" {
			t.Errorf("expected mode 'text', got %q", opts.Mode)
		}
	})
	t.Run("Invalid/unimplemented modes", func(t *testing.T) {
		invalidModes := []string{"json", "rpc", "invalid"}
		for _, m := range invalidModes {
			_, err := ParseFlags([]string{"--mode", m})
			if err == nil {
				t.Errorf("expected error for mode %q, got nil", m)
			} else if !strings.Contains(err.Error(), "is not implemented") {
				t.Errorf("unexpected error message: %v", err)
			}
		}
	})
}

func TestParseFlags_InvalidTools(t *testing.T) {
	t.Run("Invalid --tools name", func(t *testing.T) {
		_, err := ParseFlags([]string{"--tools", "read,invalid_tool"})
		if err == nil {
			t.Error("expected error for invalid tool name in --tools, got nil")
		} else if !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
	t.Run("Invalid --exclude-tools name", func(t *testing.T) {
		_, err := ParseFlags([]string{"--exclude-tools", "invalid_tool"})
		if err == nil {
			t.Error("expected error for invalid tool name in --exclude-tools, got nil")
		} else if !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestRunPrintMode_Cancellation(t *testing.T) {
	// Register a dummy provider for testing so that RunPrintMode doesn't fail on registry lookup/dispatch
	ai.ClearApiProviders()
	defer ai.ClearApiProviders()

	dummyStream := func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.StreamOptions) *ai.AssistantStream {
		stream := ai.NewAssistantStream(10)
		stream.Push(ai.AssistantMessageEvent{Type: ai.EventStart})
		stream.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: ai.StopReasonStop})
		return stream
	}
	dummyStreamSimple := func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		return dummyStream(ctx, model, c, &opts.StreamOptions)
	}

	err := ai.RegisterApiProvider(ai.ApiProvider{
		API:          ai.APIIDOpenAICodexResponses,
		Stream:       dummyStream,
		StreamSimple: dummyStreamSimple,
	})
	if err != nil {
		t.Fatalf("failed to register mock provider: %v", err)
	}

	opts := &CLIOptions{
		ModelID:     "gpt-5.4",
		PrintPrompt: "hello",
		NoTools:     true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Run print mode, should terminate gracefully or return context canceled error
	err = RunPrintMode(ctx, opts)
	if err != nil && err != context.Canceled {
		t.Errorf("expected nil or Canceled error, got %v", err)
	}
}

func TestCLI_HelpExitCode(t *testing.T) {
	if os.Getenv("BE_PI_CLI") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCLI_HelpExitCode", "--", "--help")
	cmd.Env = append(os.Environ(), "BE_PI_CLI=1")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("expected successful exit code 0 for help flag, got: %v", err)
	}
}

func TestCLI_InvalidFlagExitCode(t *testing.T) {
	if os.Getenv("BE_PI_CLI") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCLI_InvalidFlagExitCode", "--", "--invalid-flag")
	cmd.Env = append(os.Environ(), "BE_PI_CLI=1")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit code for invalid flag, got 0")
	}
}
