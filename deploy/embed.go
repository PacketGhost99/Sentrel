// Package deploy embeds the installer scripts served by the server.
package deploy

import _ "embed"

//go:embed install-agent.sh
var AgentShell []byte

//go:embed install-agent.ps1
var AgentPowerShell []byte

//go:embed install-server.sh
var ServerShell []byte
