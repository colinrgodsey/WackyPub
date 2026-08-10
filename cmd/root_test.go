package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestBundledSkillOutput(t *testing.T) {
	BundledSkill = "---\nname: wackypub\ndescription: test skill\n---\n# Test Skill\n"

	t.Run("skill subcommand", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		RootCmd.SetArgs([]string{"skill"})
		err := RootCmd.Execute()

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Fatalf("unexpected error executing skill subcommand: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		out := buf.String()

		if !strings.Contains(out, "name: wackypub") || !strings.Contains(out, "# Test Skill") {
			t.Errorf("unexpected skill output: %q", out)
		}
	})
}
