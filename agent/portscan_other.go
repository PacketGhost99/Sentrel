//go:build !linux

package agent

import "errors"

func listeningTCPPorts() ([]int, error) { return nil, errors.New("not supported on this OS") }
