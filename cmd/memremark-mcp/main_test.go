package main

import (
	"os/exec"
	"testing"
)

func TestBinaryBuild(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build memremark-mcp binary: %s", string(out))
	}
}
