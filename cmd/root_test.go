package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestBundledSkillOutput(t *testing.T) {
	BundledA2ASkill = "---\nname: wackypub-a2a\ndescription: test a2a skill\n---\n# Test A2A Skill\n"
	BundledWSSkill = "---\nname: wackypub-ws\ndescription: test ws skill\n---\n# Test WS Skill\n"

	t.Run("skill a2a subcommand", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		RootCmd.SetArgs([]string{"skill", "a2a"})
		err := RootCmd.Execute()

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Fatalf("unexpected error executing skill a2a: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		out := buf.String()

		if !strings.Contains(out, "name: wackypub-a2a") || !strings.Contains(out, "# Test A2A Skill") {
			t.Errorf("unexpected skill output: %q", out)
		}
	})

	t.Run("skill ws subcommand", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		RootCmd.SetArgs([]string{"skill", "ws"})
		err := RootCmd.Execute()

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Fatalf("unexpected error executing skill ws: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		out := buf.String()

		if !strings.Contains(out, "name: wackypub-ws") || !strings.Contains(out, "# Test WS Skill") {
			t.Errorf("unexpected skill output: %q", out)
		}
	})
}
