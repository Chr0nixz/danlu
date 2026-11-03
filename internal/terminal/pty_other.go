//go:build !linux

package terminal

func startPTY(OpenOptions) (process, error) { return nil, ErrUnsupported }
