//go:build !linux

package strict

import (
	"errors"
	"io"
)

type Target struct {
	UID  int
	Name string
}

var errUnsupported = errors.New("严格限卡只支持 Linux")

func ResolveTarget(string) (Target, error)    { return Target{}, errUnsupported }
func Install(Target, string, io.Writer) error { return errUnsupported }
func Uninstall(Target, io.Writer) error       { return errUnsupported }
