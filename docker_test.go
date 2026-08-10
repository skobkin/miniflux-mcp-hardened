package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionDockerfileKeepsScratchHealthcheck(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)
	for _, required := range []string{
		"FROM scratch",
		"USER 65532:65532",
		`HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/miniflux-mcp", "healthcheck"]`,
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
	if strings.Contains(dockerfile, "CMD-SHELL") {
		t.Fatal("Dockerfile healthcheck requires a shell")
	}
}
