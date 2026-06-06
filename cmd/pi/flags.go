package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"pi-go/pkg/ai"
)

// CLIOptions holds the parsed CLI options.
type CLIOptions struct {
	PrintPrompt        string
	Provider           string
	ModelID            string
	Thinking           string
	Tools              string
	ExcludeTools       string
	NoTools            bool
	NoBuiltinTools     bool
	Mode               string
	Verbose            bool
	Version            bool
	ListModels         bool
	SystemPrompt       string
	AppendSystemPrompt string
	Export             string
	Offline            bool
	NoContextFiles     bool
	NoSkills           bool
	Skills             string
	PositionalArgs     []string
}

// ParseFlags parses command-line arguments into a CLIOptions struct.
// It uses a custom FlagSet to support testing without modifying global flags.
func ParseFlags(args []string) (*CLIOptions, error) {
	fs := flag.NewFlagSet("pi", flag.ContinueOnError)
	opts := &CLIOptions{}

	// Flag definitions
	fs.StringVar(&opts.PrintPrompt, "p", "", "Prompt to execute in non-interactive print mode")
	var printAlias string
	fs.StringVar(&printAlias, "print", "", "Prompt to execute in non-interactive print mode (alias)")

	fs.StringVar(&opts.Provider, "provider", "openai-codex", "Provider to use (only openai-codex is supported)")
	fs.StringVar(&opts.ModelID, "model", "gpt-5.4", "Model ID to use")
	fs.StringVar(&opts.Thinking, "thinking", "", "Thinking level: off, minimal, low, medium, high, xhigh")

	fs.StringVar(&opts.Tools, "tools", "", "Comma-separated list of tools to enable")
	fs.StringVar(&opts.ExcludeTools, "exclude-tools", "", "Comma-separated list of tools to exclude")
	fs.BoolVar(&opts.NoTools, "no-tools", false, "Disable all tools")
	fs.BoolVar(&opts.NoBuiltinTools, "no-builtin-tools", false, "Disable built-in tools")

	// Planned/parity flags to prevent "flag provided but not defined" errors
	fs.StringVar(&opts.Mode, "mode", "text", "Run mode (text, json, rpc)")
	fs.BoolVar(&opts.Verbose, "verbose", false, "Enable verbose output")
	fs.BoolVar(&opts.Version, "version", false, "Show version information")

	var listModels, modelsFlag bool
	fs.BoolVar(&listModels, "list-models", false, "List supported models")
	fs.BoolVar(&modelsFlag, "models", false, "List supported models (alias)")

	fs.StringVar(&opts.SystemPrompt, "system-prompt", "", "Override system prompt")
	fs.StringVar(&opts.AppendSystemPrompt, "append-system-prompt", "", "Append to system prompt")
	fs.StringVar(&opts.Export, "export", "", "Export session to file")
	fs.BoolVar(&opts.Offline, "offline", false, "Run in offline mode")
	fs.BoolVar(&opts.NoContextFiles, "no-context-files", false, "Do not load context files")

	var noSkills, noSkill bool
	fs.BoolVar(&noSkills, "no-skills", false, "Disable skills")
	fs.BoolVar(&noSkill, "no-skill", false, "Disable skills (alias)")
	fs.StringVar(&opts.Skills, "skill", "", "Path to skill file or directory")

	// Check for forbidden flags explicitly before parsing to avoid standard flag error
	for _, arg := range args {
		if strings.HasPrefix(arg, "--api-key") || strings.HasPrefix(arg, "-api-key") {
			return nil, errors.New("flag --api-key is forbidden: OAuth/Codex subscription authentication is used")
		}
		if strings.HasPrefix(arg, "--max-turns") || strings.HasPrefix(arg, "-max-turns") {
			return nil, errors.New("flag --max-turns is not supported")
		}
	}

	// Parse the arguments
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Unify aliases/options
	if printAlias != "" && opts.PrintPrompt == "" {
		opts.PrintPrompt = printAlias
	}
	opts.ListModels = listModels || modelsFlag
	opts.NoSkills = noSkills || noSkill

	// Validation:
	// 1. Provider validation (reject others with explicit errors)
	if opts.Provider != "openai-codex" {
		return nil, fmt.Errorf("unsupported provider %q: only %q is supported", opts.Provider, "openai-codex")
	}
	// Normalize model ID if passed in provider/model format (e.g. openai-codex/gpt-5.4)
	if strings.Contains(opts.ModelID, "/") {
		parts := strings.SplitN(opts.ModelID, "/", 2)
		provPrefix := parts[0]
		if provPrefix != "openai-codex" {
			return nil, fmt.Errorf("unsupported provider prefix %q in model ID: only %q is supported", provPrefix, "openai-codex")
		}
		opts.ModelID = parts[1]
	}
	// 2. Model ID validation (must exist in Codex registry)
	model, ok := ai.GetModel(opts.ModelID)
	if !ok {
		return nil, fmt.Errorf("model %q not found in registry", opts.ModelID)
	}
	if model.Provider != ai.ProviderIDOpenAICodex {
		return nil, fmt.Errorf("model %q does not belong to provider %q", opts.ModelID, "openai-codex")
	}
	// 3. Thinking validation & clamping
	if opts.Thinking != "" {
		level := ai.ModelThinkingLevel(opts.Thinking)
		switch level {
		case ai.ModelThinkingLevelOff, ai.ModelThinkingLevelMinimal, ai.ModelThinkingLevelLow,
			ai.ModelThinkingLevelMedium, ai.ModelThinkingLevelHigh, ai.ModelThinkingLevelXHigh:
			// valid level syntax
			clamped := ai.ClampThinkingLevel(model, level)
			opts.Thinking = string(clamped)
		default:
			return nil, fmt.Errorf("invalid thinking level %q; must be one of: off, minimal, low, medium, high, xhigh", opts.Thinking)
		}
	}
	// 4. Mode validation (reject unimplemented modes)
	if opts.Mode != "text" {
		return nil, fmt.Errorf("mode %q is not implemented in this phase: only 'text' is supported", opts.Mode)
	}
	// 5. Tool names validation (reject unknown tool names)
	if opts.Tools != "" {
		parts := strings.Split(opts.Tools, ",")
		for _, p := range parts {
			name := strings.TrimSpace(p)
			if name != "" {
				if !builtInToolNames[name] {
					return nil, fmt.Errorf("unknown tool %q in --tools", name)
				}
			}
		}
	}
	if opts.ExcludeTools != "" {
		parts := strings.Split(opts.ExcludeTools, ",")
		for _, p := range parts {
			name := strings.TrimSpace(p)
			if name != "" {
				if !builtInToolNames[name] {
					return nil, fmt.Errorf("unknown tool %q in --exclude-tools", name)
				}
			}
		}
	}
	opts.PositionalArgs = fs.Args()

	// If no explicit print prompt, treat joined positional arguments as prompt
	if opts.PrintPrompt == "" && len(opts.PositionalArgs) > 0 {
		// Only if the first positional argument is not a known subcommand
		firstArg := opts.PositionalArgs[0]
		if firstArg != "config" {
			opts.PrintPrompt = strings.Join(opts.PositionalArgs, " ")
		}
	}

	return opts, nil
}
