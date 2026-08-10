package main

import (
	"fmt"
	"strings"
	"sync"
)

const writeToolsEnvironmentVariable = "MCP_WRITE_TOOLS"

type writeToolSet map[string]struct{}

func (s writeToolSet) contains(name string) bool {
	_, ok := s[name]

	return ok
}

var approvedWriteTools = sync.OnceValue(func() writeToolSet {
	result := make(writeToolSet)
	for _, definition := range (&MinifluxServer{}).writeToolDefinitions() {
		result[definition.Tool.Name] = struct{}{}
	}

	return result
})

func parseWriteTools(value string) (writeToolSet, error) {
	result := make(writeToolSet)
	if strings.TrimSpace(value) == "" {
		return result, nil
	}

	approved := approvedWriteTools()
	for _, configuredName := range strings.Split(value, ",") {
		name := strings.TrimSpace(configuredName)
		if name == "" {
			return nil, fmt.Errorf("%s contains an empty tool name", writeToolsEnvironmentVariable)
		}
		if !approved.contains(name) {
			return nil, fmt.Errorf("%s contains unknown or disallowed tool %q", writeToolsEnvironmentVariable, name)
		}
		result[name] = struct{}{}
	}

	return result, nil
}
