package main

import (
	"os"
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
		{name: "bulk", value: "update_entries_status", expected: []string{"update_entries_status"}},
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
		"get_unread_digest",
		"get_version",
		"healthcheck",
	}
	actual := toolNames((&MinifluxServer{}).toolDefinitions(writeToolSet{}))
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("default tools = %v, want %v", actual, expected)
	}
}

func TestEachAllowedWriteToolCanBeEnabled(t *testing.T) {
	expected := []string{"refresh_feed", "toggle_starred", "update_entries_status", "update_entry_status"}
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
			if len(names) != 15 {
				t.Fatalf("registered %d tools, want 15", len(names))
			}
		})
	}
}

func TestAgentSkillMentionsEveryTool(t *testing.T) {
	content, err := os.ReadFile("skills/miniflux-mcp-triage/SKILL.md")
	if err != nil {
		t.Fatalf("read agent skill: %v", err)
	}

	server := &MinifluxServer{}
	definitions := append(server.readToolDefinitions(), server.writeToolDefinitions()...)
	for _, definition := range definitions {
		name := definition.Tool.Name
		if !strings.Contains(string(content), "`"+name+"`") {
			t.Errorf("agent skill does not mention tool %q", name)
		}
	}
}

func TestMixedWriteToolAllowlist(t *testing.T) {
	enabled := writeToolSet{
		"update_entry_status":   {},
		"update_entries_status": {},
		"refresh_feed":          {},
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
		"update_entry_status":   {},
		"update_entries_status": {},
		"toggle_starred":        {},
		"refresh_feed":          {},
	}
	for _, definition := range (&MinifluxServer{}).toolDefinitions(enabled) {
		walkSchemaKeys(definition.Tool.InputSchema.Properties, func(key string) {
			if _, found := forbidden[strings.ToLower(key)]; found {
				t.Errorf("tool %q schema exposes forbidden property %q", definition.Tool.Name, key)
			}
		})
	}
}

func allWriteTools() writeToolSet {
	return writeToolSet{
		"update_entry_status":   {},
		"update_entries_status": {},
		"toggle_starred":        {},
		"refresh_feed":          {},
	}
}

// structuredToolNames lists every tool whose success results carry
// structuredContent and therefore must declare an output schema.
func structuredToolNames() map[string]struct{} {
	return map[string]struct{}{
		"fetch_counters":        {},
		"get_categories":        {},
		"get_category_entries":  {},
		"get_category_entry":    {},
		"get_category_feeds":    {},
		"get_entries":           {},
		"get_entry":             {},
		"get_feed":              {},
		"get_feed_entries":      {},
		"get_feed_entry":        {},
		"get_feeds":             {},
		"get_unread_digest":     {},
		"get_version":           {},
		"update_entries_status": {},
	}
}

func TestStructuredToolsAdvertiseOutputSchemas(t *testing.T) {
	structured := structuredToolNames()
	for _, definition := range (&MinifluxServer{}).toolDefinitions(allWriteTools()) {
		name := definition.Tool.Name
		if _, isStructured := structured[name]; isStructured {
			delete(structured, name)
			schema := definition.Tool.OutputSchema
			if schema.Type != "object" || len(schema.Properties) == 0 {
				t.Errorf("tool %q output schema = %#v, want a generated object schema; mcp-go schema generation may have failed silently", name, schema)
			}

			continue
		}
		if definition.Tool.OutputSchema.Type != "" || len(definition.Tool.OutputSchema.Properties) != 0 {
			t.Errorf("tool %q declares an unexpected output schema", name)
		}
	}
	if len(structured) != 0 {
		t.Errorf("structured tools missing from definitions: %v", structured)
	}
}

func TestOutputSchemasDoNotExposeSecrets(t *testing.T) {
	forbidden := map[string]struct{}{
		"username":             {},
		"password":             {},
		"cookie":               {},
		"proxy_url":            {},
		"api_key":              {},
		"token":                {},
		"apprise_service_urls": {},
		"secret":               {},
		"authorization":        {},
	}
	for _, definition := range (&MinifluxServer{}).toolDefinitions(allWriteTools()) {
		if len(definition.Tool.OutputSchema.Properties) == 0 {
			continue
		}
		walkSchemaKeys(definition.Tool.OutputSchema.Properties, func(key string) {
			if _, found := forbidden[strings.ToLower(key)]; found {
				t.Errorf("tool %q output schema exposes forbidden property %q", definition.Tool.Name, key)
			}
		})
	}
}

func TestOutputSchemasMatchDTOShapes(t *testing.T) {
	type expectation struct {
		required   []string
		omittedReq []string
	}
	expectedByName := map[string]expectation{
		"get_version":           {required: []string{"version"}},
		"fetch_counters":        {required: []string{"reads", "unreads"}},
		"get_feeds":             {required: []string{"feeds"}},
		"get_category_feeds":    {required: []string{"feeds"}},
		"get_categories":        {required: []string{"categories"}},
		"get_entries":           {required: []string{"entries", "total"}},
		"get_unread_digest":     {required: []string{"ack_entry_ids", "content_format", "entries", "scan_truncated"}, omittedReq: []string{"response_size_limited"}},
		"update_entries_status": {required: []string{"status", "updated"}},
		"get_feed":              {required: []string{"description", "disabled", "id", "language", "parsing_error_count", "site_url", "title"}, omittedReq: []string{"category", "checked_at", "next_check_at"}},
	}
	definitions := (&MinifluxServer{}).toolDefinitions(allWriteTools())
	for _, definition := range definitions {
		expected, known := expectedByName[definition.Tool.Name]
		if !known {
			continue
		}
		delete(expectedByName, definition.Tool.Name)
		actual := append([]string(nil), definition.Tool.OutputSchema.Required...)
		sort.Strings(actual)
		expectedSorted := append([]string(nil), expected.required...)
		sort.Strings(expectedSorted)
		if !reflect.DeepEqual(actual, expectedSorted) {
			t.Errorf("tool %q output schema required = %v, want %v", definition.Tool.Name, actual, expectedSorted)
		}
		for _, omitted := range expected.omittedReq {
			if containsString(actual, omitted) {
				t.Errorf("tool %q output schema marks omitempty field %q as required", definition.Tool.Name, omitted)
			}
		}
	}
	if len(expectedByName) != 0 {
		t.Errorf("tools missing from definitions: %v", expectedByName)
	}
}

func TestCounterOutputSchemaUsesStringKeys(t *testing.T) {
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if definition.Tool.Name != "fetch_counters" {
			continue
		}
		reads, ok := definition.Tool.OutputSchema.Properties["reads"].(map[string]interface{})
		if !ok {
			t.Fatal("fetch_counters output schema has no reads property; mcp-go schema generation failed silently")
		}
		if reads["type"] != "object" {
			t.Fatalf("reads schema type = %v, want object for a counter map", reads["type"])
		}
		additional, ok := reads["additionalProperties"].(map[string]interface{})
		if !ok || additional["type"] != "integer" {
			t.Fatalf("reads additionalProperties = %#v, want integer values", reads["additionalProperties"])
		}

		return
	}
	t.Fatal("fetch_counters definition not found")
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

func TestEntryEnumSchemasMatchRuntimePolicy(t *testing.T) {
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if definition.Tool.Name != "get_entries" {
			continue
		}

		properties := definition.Tool.InputSchema.Properties
		statuses := properties["statuses"].(map[string]interface{})
		if statuses["maxItems"] != len(entryFilterStatuses) || statuses["uniqueItems"] != true {
			t.Fatalf("statuses schema = %#v", statuses)
		}
		statusItems := statuses["items"].(map[string]interface{})
		if !reflect.DeepEqual(statusItems["enum"], entryFilterStatuses) {
			t.Errorf("statuses enum = %v, want %v", statusItems["enum"], entryFilterStatuses)
		}
		if !reflect.DeepEqual(properties["status"].(map[string]interface{})["enum"], entryFilterStatuses) {
			t.Errorf("status enum does not match runtime values")
		}
		if !reflect.DeepEqual(properties["order"].(map[string]interface{})["enum"], entryOrderValues) {
			t.Errorf("order enum does not match runtime values")
		}
		if !reflect.DeepEqual(properties["direction"].(map[string]interface{})["enum"], entryDirectionValues) {
			t.Errorf("direction enum does not match runtime values")
		}

		return
	}
	t.Fatal("get_entries definition not found")
}

func TestDigestLimitSchemaMatchesRuntimePolicy(t *testing.T) {
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if definition.Tool.Name != "get_unread_digest" {
			continue
		}
		limit, ok := definition.Tool.InputSchema.Properties["limit"].(map[string]interface{})
		if !ok {
			t.Fatal("get_unread_digest has no limit schema")
		}
		if limit["default"] != defaultDigestEntryLimit {
			t.Errorf("digest limit default = %v, want %d", limit["default"], defaultDigestEntryLimit)
		}
		if limit["maximum"] != maximumDigestEntryLimit {
			t.Errorf("digest limit maximum = %v, want %d", limit["maximum"], maximumDigestEntryLimit)
		}

		return
	}
	t.Fatal("get_unread_digest definition not found")
}

func TestDetailToolSchemasExposeContentOffset(t *testing.T) {
	wanted := map[string]struct{}{
		"get_entry":          {},
		"get_feed_entry":     {},
		"get_category_entry": {},
	}
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if _, ok := wanted[definition.Tool.Name]; !ok {
			continue
		}
		offset, ok := definition.Tool.InputSchema.Properties["content_offset"].(map[string]interface{})
		if !ok {
			t.Fatalf("tool %q has no content_offset schema", definition.Tool.Name)
		}
		if offset["minimum"] != 0 {
			t.Errorf("tool %q content_offset minimum = %v, want 0", definition.Tool.Name, offset["minimum"])
		}
		if offset["maximum"] != maximumSafeJSONInteger {
			t.Errorf("tool %q content_offset maximum = %v, want %d", definition.Tool.Name, offset["maximum"], maximumSafeJSONInteger)
		}
		delete(wanted, definition.Tool.Name)
	}
	if len(wanted) != 0 {
		t.Fatalf("detail tools missing from definitions: %v", wanted)
	}
}

func TestSearchSchemaMatchesRuntimePolicy(t *testing.T) {
	for _, definition := range (&MinifluxServer{}).readToolDefinitions() {
		if definition.Tool.Name != "get_entries" {
			continue
		}
		search, ok := definition.Tool.InputSchema.Properties["search"].(map[string]interface{})
		if !ok {
			t.Fatal("get_entries search schema missing")
		}
		if search["maxLength"] != maximumFreeFormStringLength {
			t.Fatalf("search maxLength = %v, want %d", search["maxLength"], maximumFreeFormStringLength)
		}

		return
	}
	t.Fatal("get_entries definition not found")
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

func TestToolAnnotationsMatchPolicy(t *testing.T) {
	enabled := writeToolSet{
		"update_entry_status":   {},
		"update_entries_status": {},
		"toggle_starred":        {},
		"refresh_feed":          {},
	}
	type expectedAnnotations struct {
		readOnly    bool
		destructive bool
		idempotent  bool
		openWorld   bool
	}
	read := expectedAnnotations{readOnly: true, destructive: false, idempotent: true, openWorld: true}
	statusWrite := expectedAnnotations{readOnly: false, destructive: false, idempotent: true, openWorld: true}
	nonIdempotentWrite := expectedAnnotations{readOnly: false, destructive: false, idempotent: false, openWorld: true}
	expectedByName := map[string]expectedAnnotations{
		"get_feeds":             read,
		"get_feed":              read,
		"get_feed_entries":      read,
		"get_feed_entry":        read,
		"get_entries":           read,
		"get_unread_digest":     read,
		"get_entry":             read,
		"get_categories":        read,
		"get_category_feeds":    read,
		"get_category_entries":  read,
		"get_category_entry":    read,
		"get_version":           read,
		"healthcheck":           read,
		"fetch_counters":        read,
		"update_entry_status":   statusWrite,
		"update_entries_status": statusWrite,
		"refresh_feed":          nonIdempotentWrite,
		"toggle_starred":        nonIdempotentWrite,
	}
	seen := make(map[string]struct{}, len(expectedByName))
	for _, definition := range (&MinifluxServer{}).toolDefinitions(enabled) {
		expected, known := expectedByName[definition.Tool.Name]
		if !known {
			t.Errorf("tool %q has no expected annotation policy; extend TestToolAnnotationsMatchPolicy", definition.Tool.Name)

			continue
		}
		seen[definition.Tool.Name] = struct{}{}
		annotations := definition.Tool.Annotations
		for _, hint := range []struct {
			name     string
			actual   *bool
			expected bool
		}{
			{name: "readOnlyHint", actual: annotations.ReadOnlyHint, expected: expected.readOnly},
			{name: "destructiveHint", actual: annotations.DestructiveHint, expected: expected.destructive},
			{name: "idempotentHint", actual: annotations.IdempotentHint, expected: expected.idempotent},
			{name: "openWorldHint", actual: annotations.OpenWorldHint, expected: expected.openWorld},
		} {
			if hint.actual == nil || *hint.actual != hint.expected {
				t.Errorf("tool %q %s = %v, want %v", definition.Tool.Name, hint.name, hint.actual, hint.expected)
			}
		}
	}
	for name := range expectedByName {
		if _, ok := seen[name]; !ok {
			t.Errorf("expected tool %q is not registered", name)
		}
	}
}
