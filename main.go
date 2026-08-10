package main

import (
	_ "embed"

	"github.com/colinrgodsey/WackyPubAI/cmd"
)

//go:embed skills/wackypub/SKILL.md
var bundledWackyPubSkill string

func main() {
	cmd.BundledSkill = bundledWackyPubSkill
	cmd.Execute()
}
