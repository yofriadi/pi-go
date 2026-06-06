package tools

import "pi-go/pkg/ai"

// ReadToolDefinition defines the schema for the read tool.
var ReadToolDefinition = ai.ToolDefinition{
	Name:        "read",
	Description: "Read the content of a file, with optional 1-indexed offset and limit.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional 1-indexed line offset to start reading from.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of lines to read.",
			},
		},
		"required": []any{"path"},
	},
}

// WriteToolDefinition defines the schema for the write tool.
var WriteToolDefinition = ai.ToolDefinition{
	Name:        "write",
	Description: "Write content to a file, recursively creating parent directories.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file.",
			},
		},
		"required": []any{"path", "content"},
	},
}

// EditToolDefinition defines the schema for the edit tool.
var EditToolDefinition = ai.ToolDefinition{
	Name:        "edit",
	Description: "Edit a file by applying one or more replacements.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit.",
			},
			"edits": map[string]any{
				"oneOf": []any{
					map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"oldText": map[string]any{
									"type":        "string",
									"description": "The exact text to be replaced.",
								},
								"newText": map[string]any{
									"type":        "string",
									"description": "The text to replace it with.",
								},
							},
							"required": []any{"oldText", "newText"},
						},
					},
					map[string]any{
						"type": "string",
					},
				},
				"description": "List of replacement operations as an array of objects or a JSON-serialized string.",
			},
			"oldText": map[string]any{
				"type":        "string",
				"description": "Legacy/fallback parameter: the exact text to replace.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Legacy/fallback parameter: the text to replace it with.",
			},
		},
		"required": []any{"path"},
	},
}

// BashToolDefinition defines the schema for the bash tool.
var BashToolDefinition = ai.ToolDefinition{
	Name:        "bash",
	Description: "Execute a command in the platform shell.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to run.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in seconds.",
			},
		},
		"required": []any{"command"},
	},
}
