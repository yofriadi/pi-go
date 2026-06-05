package ai

import "reflect"

// DeepCopy creates a complete deep copy of the UserMessage.
func (m UserMessage) DeepCopy() Message {
	return UserMessage{
		Content:   deepCopyUserContent(m.Content),
		Timestamp: m.Timestamp,
	}
}

func deepCopyUserContent(c any) any {
	if c == nil {
		return nil
	}
	switch val := c.(type) {
	case string:
		return val
	case []UserContent:
		return deepCopyUserContentSlice(val)
	default:
		return val
	}
}

func deepCopyUserContentSlice(src []UserContent) []UserContent {
	if src == nil {
		return nil
	}
	res := make([]UserContent, len(src))
	for i, item := range src {
		if item != nil {
			res[i] = item.deepCopyUserContent()
		}
	}
	return res
}

// DeepCopy creates a complete deep copy of the AssistantMessage.
func (m *AssistantMessage) DeepCopy() Message {
	if m == nil {
		return nil
	}

	var contentCopy []AssistantContent
	if m.Content != nil {
		contentCopy = deepCopyAssistantContentSlice(m.Content)
	}

	var diagnosticsCopy []AssistantMessageDiagnostic
	if m.Diagnostics != nil {
		diagnosticsCopy = make([]AssistantMessageDiagnostic, len(m.Diagnostics))
		for i, d := range m.Diagnostics {
			diagnosticsCopy[i] = AssistantMessageDiagnostic{
				Code:     d.Code,
				Message:  d.Message,
				Severity: d.Severity,
				Details:  deepCopyValue(d.Details),
			}
		}
	}

	return &AssistantMessage{
		Content:       contentCopy,
		API:           m.API,
		Provider:      m.Provider,
		Model:         m.Model,
		ResponseModel: m.ResponseModel,
		ResponseID:    m.ResponseID,
		Diagnostics:   diagnosticsCopy,
		Usage:         m.Usage,
		StopReason:    m.StopReason,
		ErrorMessage:  m.ErrorMessage,
		Timestamp:     m.Timestamp,
	}
}

func deepCopyAssistantContentSlice(src []AssistantContent) []AssistantContent {
	if src == nil {
		return nil
	}
	res := make([]AssistantContent, len(src))
	for i, item := range src {
		if item != nil {
			res[i] = item.deepCopyAssistantContent()
		}
	}
	return res
}

// DeepCopy creates a complete deep copy of the ToolResultMessage.
func (m ToolResultMessage) DeepCopy() Message {
	var contentCopy []ToolResultContent
	if m.Content != nil {
		contentCopy = deepCopyToolResultContentSlice(m.Content)
	}
	return ToolResultMessage{
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
		Content:    contentCopy,
		Details:    deepCopyValue(m.Details),
		IsError:    m.IsError,
		Timestamp:  m.Timestamp,
	}
}

func deepCopyToolResultContentSlice(src []ToolResultContent) []ToolResultContent {
	if src == nil {
		return nil
	}
	res := make([]ToolResultContent, len(src))
	for i, item := range src {
		if item != nil {
			res[i] = item.deepCopyToolResultContent()
		}
	}
	return res
}

func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		res := make([]any, len(val))
		for i, item := range val {
			res[i] = deepCopyValue(item)
		}
		return res
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Map:
			if rv.IsNil() {
				return nil
			}
			cp := reflect.MakeMapWithSize(rv.Type(), rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				cpVal := deepCopyValue(iter.Value().Interface())
				if cpVal != nil {
					cp.SetMapIndex(iter.Key(), reflect.ValueOf(cpVal))
				} else {
					cp.SetMapIndex(iter.Key(), reflect.Zero(rv.Type().Elem()))
				}
			}
			return cp.Interface()
		case reflect.Slice:
			if rv.IsNil() {
				return nil
			}
			cp := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
			for i := range rv.Len() {
				cpVal := deepCopyValue(rv.Index(i).Interface())
				if cpVal != nil {
					cp.Index(i).Set(reflect.ValueOf(cpVal))
				}
			}
			return cp.Interface()
		default:
			return v
		}
	}
}

func deepCopyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	res := make(map[string]any, len(src))
	for k, v := range src {
		res[k] = deepCopyValue(v)
	}
	return res
}
