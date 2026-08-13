package main

import (
	_ "embed"

	"github.com/colinrgodsey/WackyPub/cmd"
)

//go:embed skills/wackypub-a2a/SKILL.md
var bundledA2ASkill string

//go:embed skills/wackypub-ws/SKILL.md
var bundledWSSkill string

func main() {
	cmd.BundledA2ASkill = bundledA2ASkill
	cmd.BundledWSSkill = bundledWSSkill
	cmd.Execute()
}
