package comms

import _ "embed"

// Skill is provisioned with the runtime connection, not copied into domain agents.
//
//go:embed skill/SKILL.md
var Skill string
