package main

import (
	"fmt"
	"strings"
)

const writeToolsEnvironmentVariable = "MCP_WRITE_TOOLS"

var allowedWriteToolNames = map[string]struct{}{
	"update_entry_status": {},
	"toggle_starred":      {},
	"refresh_feed":        {},
}

type writeToolSet map[string]struct{}

func (s writeToolSet) contains(name string) bool {
	_, ok := s[name]
	return ok
}

func parseWriteTools(value string) (writeToolSet, error) {
	result := make(writeToolSet)
	if strings.TrimSpace(value) == "" {
		return result, nil
	}

	for _, configuredName := range strings.Split(value, ",") {
		name := strings.TrimSpace(configuredName)
		if name == "" {
			return nil, fmt.Errorf("%s contains an empty tool name", writeToolsEnvironmentVariable)
		}
		if _, ok := allowedWriteToolNames[name]; !ok {
			return nil, fmt.Errorf("%s contains unknown or disallowed tool %q", writeToolsEnvironmentVariable, name)
		}
		result[name] = struct{}{}
	}
	return result, nil
}
