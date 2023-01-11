//go:build freebsd
// +build freebsd

/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mount

/*
#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <sys/_iovec.h>
#include <sys/mount.h>
#include <sys/param.h>
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unsafe"

	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"
)

// Mounter implements mount.Interface for unsupported platforms
type Mounter struct {
	mounterPath string
}

//var errUnsupported = errors.New("util/mount on this platform is not supported")

func getMounts() ([]unix.Statfs_t, error) {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	mounts := make([]unix.Statfs_t, count)
	count, err = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}

	return mounts, nil
}

func allocateIOVecs(options []string) []C.struct_iovec {
	out := make([]C.struct_iovec, len(options))
	for i, option := range options {
		out[i].iov_base = unsafe.Pointer(C.CString(option))
		out[i].iov_len = C.size_t(len(option) + 1)
	}
	return out
}

func mount(device, target, mType string, flag uintptr, options []string) error {
	isNullFS := false

	nmOptions := []string{"fspath", target}

	for _, option := range options {
		if option == "bind" {
			isNullFS = true
			continue
		}
		opt := strings.SplitN(option, "=", 2)
		nmOptions = append(nmOptions, opt[0])
		if len(opt) == 2 {
			nmOptions = append(nmOptions, opt[1])
		} else {
			nmOptions = append(nmOptions, "")
		}
	}

	if isNullFS {
		nmOptions = append(nmOptions, "fstype", "nullfs", "target", device)
	} else {
		nmOptions = append(nmOptions, "fstype", mType, "from", device)
	}
	rawOptions := allocateIOVecs(nmOptions)
	for _, rawOption := range rawOptions {
		defer C.free(rawOption.iov_base)
	}

	if errno := C.nmount(&rawOptions[0], C.uint(len(nmOptions)), C.int(flag)); errno != 0 {
		reason := C.GoString(C.strerror(*C.__error()))
		return fmt.Errorf("failed to call nmount: %s", reason)
	}
	return nil
}

// New returns a mount.Interface for the current system.
// It provides options to override the default mounter behavior.
// mounterPath allows using an alternative to `/bin/mount` for mounting.
func New(mounterPath string) Interface {
	return &Mounter{
		mounterPath: mounterPath,
	}
}

func (mounter *Mounter) Mount(source string, target string, fstype string, options []string) error {
	return mounter.MountSensitive(source, target, fstype, options, nil /* sensitiveOptions */)
}

func (mounter *Mounter) MountSensitiveWithoutSystemd(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return mounter.MountSensitive(source, target, fstype, options, sensitiveOptions /* sensitiveOptions */)
}

func (mounter *Mounter) MountSensitiveWithoutSystemdWithMountFlags(source string, target string, fstype string, options []string, sensitiveOptions []string, mountFlags []string) error {
	return mounter.MountSensitive(source, target, fstype, options, sensitiveOptions /* sensitiveOptions */)
}

func (mounter *Mounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	// TODO: parse options to flags
	return mount(source, target, fstype, 0, append(options, sensitiveOptions...))
}

// Unmount always returns an error on unsupported platforms
func (mounter *Mounter) Unmount(target string) error {
	err := unix.Unmount(target, unix.MNT_FORCE)
	switch err {
	case unix.EINVAL, nil:
		// Ignore "not mounted" error here. Note the same error
		// can be returned if flags are invalid, so this code
		// assumes that the flags value is always correct.
		return nil
	}
	return err
}

// List always returns an error on unsupported platforms
func (mounter *Mounter) List() ([]MountPoint, error) {
	//return []MountPoint{}, errUnsupported
	return nil, errors.New("mount.List unsupported")
}

func (mounter *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	mounts, err := getMounts()
	if err != nil {
		return true, err
	}
	for _, m := range mounts {
		mountPoint := string(m.Mntonname[:bytes.IndexByte(m.Mntonname[:], 0)])
		if file == mountPoint {
			return false, nil
		}
	}
	return true, nil
}

// CanSafelySkipMountPointCheck always returns false on unsupported platforms
func (mounter *Mounter) CanSafelySkipMountPointCheck() bool {
	return false
}

// IsMountPoint determines if a directory is a mountpoint.
// It always returns an error on unsupported platforms.
func (mounter *Mounter) IsMountPoint(file string) (bool, error) {
	isMnt, isMntErr := mountinfo.Mounted(file)
	if isMntErr == nil {
		return isMnt, nil
	}
	if isMntErr != nil {
		if errors.Is(isMntErr, fs.ErrNotExist) {
			return false, fs.ErrNotExist
		}
		// We were not allowed to do the simple stat() check, e.g. on NFS with
		// root_squash. Fall back to /proc/mounts check below when
		// fs.ErrPermission is returned.
		if !errors.Is(isMntErr, fs.ErrPermission) {
			return false, isMntErr
		}
	}
	return false, nil
}

// GetMountRefs always returns an error on unsupported platforms
func (mounter *Mounter) GetMountRefs(pathname string) ([]string, error) {
	//return nil, errUnsupported
	return nil, errors.New("mount.GetMountRefs unsupported")
}

func (mounter *SafeFormatAndMount) formatAndMountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string, formatOptions []string) error {
	return mounter.Interface.Mount(source, target, fstype, options)
}

func (mounter *SafeFormatAndMount) diskLooksUnformatted(disk string) (bool, error) {
	//return true, errUnsupported
	return true, errors.New("mount.diskLooksUnformatted unsupported")
}

// IsMountPoint determines if a directory is a mountpoint.
// It always returns an error on unsupported platforms.
func (mounter *SafeFormatAndMount) IsMountPoint(file string) (bool, error) {
	//return false, errUnsupported
	return false, errors.New("mount.IsMountPoint unsupported")
}
