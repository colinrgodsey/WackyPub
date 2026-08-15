package main

import (
	_ "embed"

	"github.com/colinrgodsey/wackypub/cmd"
	adkAgent "github.com/colinrgodsey/wackypub/pkg/agent"
)

//go:embed skills/wackypub-a2a/SKILL.md
var bundledA2ASkill string

//go:embed skills/wackypub-ws/SKILL.md
var bundledWSSkill string

// bundledDefaultCompactMD is examples/COMPACT.md, the default compaction
// directive shipped in the binary (D45). Assigned directly into pkg/agent's
// own DefaultCompactMD var rather than staying at the cmd layer like the two
// skills above, since LoadCompactConfig (deep inside pkg/agent) needs it
// directly, not just CLI-level commands.
//
//go:embed examples/COMPACT.md
var bundledDefaultCompactMD string

func main() {
	cmd.BundledA2ASkill = bundledA2ASkill
	cmd.BundledWSSkill = bundledWSSkill
	adkAgent.DefaultCompactMD = bundledDefaultCompactMD
	cmd.Execute()
}
