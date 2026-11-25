//go:build !linux

package localsock

import "net"

func Listen(string) (net.Listener, error) { return nil, ErrUnsupported }

func PeerUID(net.Conn) (int, bool) { return 0, false }
