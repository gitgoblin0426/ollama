//go:build !darwin

package llm

import (
	"fmt"

	"github.com/jmorganca/ollama/api"
)

func newDefaultExtServer(model string, adapters, projectors []string, opts api.Options) (extServer, error) {
	// On windows and linux we always load the llama.cpp libraries dynamically to avoid startup DLL dependencies
	// This ensures we can update the PATH at runtime to get everything loaded

	// This should never happen as we'll always try to load one or more cpu dynamic libaries before hitting default
	return nil, fmt.Errorf("no available default llm library")
}
