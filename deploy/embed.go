// Package deploy embeds the installer scripts served by the server.
package deploy

import _ "embed"

//go:embed linux/install-agent.sh
var AgentShell []byte

//go:embed windows/install-agent.ps1
var AgentPowerShell []byte

//go:embed linux/install-server.sh
var ServerShell []byte
