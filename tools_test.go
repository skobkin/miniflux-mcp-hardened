package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseWriteTools(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  []string
		wantError bool
	}{
		{name: "unset", value: "", expected: []string{}},
		{name: "whitespace", value: "  ", expected: []string{}},
		{name: "one", value: "update_entry_status", expected: []string{"update_entry_status"}},
		{name: "mixed", value: " toggle_starred, refresh_feed, toggle_starred ", expected: []string{"refresh_feed", "toggle_starred"}},
		{name: "unknown", value: "create_feed", wantError: true},
		{name: "removed admin tool", value: "flush_history", wantError: true},
		{name: "case mismatch", value: "Refresh_Feed", wantError: true},
		{name: "empty element", value: "refresh_feed,,toggle_starred", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseWriteTools(test.value)
			if test.wantError {
				if err == nil {
					t.Fatalf("parseWriteTools(%q) succeeded, want error", test.value)
				}

				return
			}
			if err != nil {
				t.Fatalf("parseWriteTools(%q): %v", test.value, err)
			}
			actualNames := make([]string, 0, len(actual))
			for name := range actual {
				actualNames = append(actualNames, name)
			}
			sort.Strings(actualNames)
			sort.Strings(test.expected)
			if !reflect.DeepEqual(actualNames, test.expected) {
				t.Fatalf("parseWriteTools(%q) = %v, want %v", test.value, actualNames, test.expected)
			}
		})
	}
}

func TestToolDefinitionsAreReadOnlyByDefault(t *testing.T) {
	expected := []string{
		"fetch_counters",
		"get_categories",
		"get_category_entries",
		"get_category_entry",
		"get_category_feeds",
		"get_entries",
		"get_entry",
		"get_feed",
		"get_feed_entries",
		"get_feed_entry",
		"get_feeds",
		"get_version",
		"healthcheck",
	}
	actual := toolNames((&MinifluxServer{}).toolDefinitions(writeToolSet{}))
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("default tools = %v, want %v", actual, expected)
	}
}

func TestEachAllowedWriteToolCanBeEnabled(t *testing.T) {
	expected := []string{"refresh_feed", "toggle_starred", "update_entry_status"}
	definitions := (&MinifluxServer{}).writeToolDefinitions()
	if actual := toolNames(definitions); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("approved write tools = %v, want %v", actual, expected)
	}

	for _, definition := range definitions {
		name := definition.Tool.Name
		t.Run(name, func(t *testing.T) {
			enabled, err := parseWriteTools(name)
			if err != nil {
				t.Fatalf("parseWriteTools(%q): %v", name, err)
			}
			names := toolNames((&MinifluxServer{}).toolDefinitions(enabled))
			if !containsString(names, name) {
				t.Fatalf("enabled tool %q not registered: %v", name, names)
			}
			if len(names) != 14 {
				t.Fatalf("registered %d tools, want 14", len(names))
			}
		})
	}
}

func TestMixedWriteToolAllowlist(t *testing.T) {
	enabled := writeToolSet{
		"update_entry_status": {},
		"refresh_feed":        {},
	}
	names := toolNames((&MinifluxServer{}).toolDefinitions(enabled))
	if !containsString(names, "update_entry_status") || !containsString(names, "refresh_feed") {
		t.Fatalf("mixed write tools missing from registry: %v", names)
	}
	if containsString(names, "toggle_starred") {
		t.Fatalf("disabled write tool registered: %v", names)
	}
}

func TestRegisteredSchemasDoNotAcceptSecrets(t *testing.T) {
	forbidden := map[string]struct{}{
		"username":             {},
		"password":             {},
		"cookie":               {},
		"proxy_url":            {},
		"api_key":              {},
		"token":                {},
		"apprise_service_urls": {},
	}
	enabled := writeToolSet{
		"update_entry_status": {},
		"toggle_starred":      {},
		"refresh_feed":        {},
	}
	for _, definition := range (&MinifluxServer{}).toolDefinitions(enabled) {
		walkSchemaKeys(definition.Tool.InputSchema.Properties, func(key string) {
			if _, found := forbidden[strings.ToLower(key)]; found {
				t.Errorf("tool %q schema exposes forbidden property %q", definition.Tool.Name, key)
			}
		})
	}
}

func TestEntryLimitSchemasMatchRuntimePolicy(t *testing.T) {
	listTools := map[string]struct{}{
		"get_entries":          {},
		"get_feed_entries":     {},
		"get_category_entries": {},
	}
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if _, ok := listTools[definition.Tool.Name]; !ok {
			continue
		}
		limit, ok := definition.Tool.InputSchema.Properties["limit"].(map[string]interface{})
		if !ok {
			t.Fatalf("tool %q has no limit schema", definition.Tool.Name)
		}
		if limit["default"] != defaultEntryLimit {
			t.Errorf("tool %q limit default = %v, want %d", definition.Tool.Name, limit["default"], defaultEntryLimit)
		}
		if limit["maximum"] != maximumEntryLimit {
			t.Errorf("tool %q limit maximum = %v, want %d", definition.Tool.Name, limit["maximum"], maximumEntryLimit)
		}
		delete(listTools, definition.Tool.Name)
	}
	if len(listTools) != 0 {
		t.Fatalf("list tools missing from definitions: %v", listTools)
	}
}

func toolNames(definitions []ToolDefinition) []string {
	result := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, definition.Tool.Name)
	}
	sort.Strings(result)

	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}

func walkSchemaKeys(value interface{}, visit func(string)) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			visit(key)
			walkSchemaKeys(child, visit)
		}
	case []interface{}:
		for _, child := range typed {
			walkSchemaKeys(child, visit)
		}
	}
}
