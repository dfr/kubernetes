//go:build freebsd

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

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

// Mounter implements mount.Interface for unsupported platforms
type Mounter struct {
	mounterPath string
}

func isBindMount(options []string) bool {
	for _, option := range options {
		if option == "bind" {
			return true
		}
	}
	return false
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
	if isBindMount(options) {
		fstype = "nullfs"
	}
	mountCmd := defaultMountCommand
	if mounter.mounterPath != "" {
		mountCmd = mounter.mounterPath
	}
	return mounter.doMount(mountCmd, source, target, fstype, options, sensitiveOptions, nil /* mountFlags */)
}

// Unmount always returns an error on unsupported platforms
func (mounter *Mounter) Unmount(target string) error {
	klog.V(4).Infof("Unmounting %s", target)
	command := exec.Command("umount", "-f", target)
	output, err := command.CombinedOutput()
	if err != nil {
		return checkUmountError(target, command, output, err)
	}
	return nil
}

// List always returns an error on unsupported platforms
func (mounter *Mounter) List() ([]MountPoint, error) {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	mounts := make([]unix.Statfs_t, count)
	count, err = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}

	res := make([]MountPoint, 0, count)
	for _, entry := range mounts {
		res = append(res, MountPoint{
			Device: unix.ByteSliceToString(entry.Mntfromname[:]),
			Path:   unix.ByteSliceToString(entry.Mntonname[:]),
			Type:   unix.ByteSliceToString(entry.Fstypename[:]),
		})
	}

	return res, nil
}

func (mounter *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	isMountPoint, err := mounter.IsMountPoint(file)
	if err != nil {
		return false, err
	}
	return !isMountPoint, nil
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

// doMount runs the mount command. sensitiveOptions is an extension of options
// except they will not be logged (because they may contain sensitive material)
// systemdMountRequired is an extension of option to decide whether uses systemd
// mount.
func (mounter *Mounter) doMount(mountCmd string, source string, target string, fstype string, options []string, sensitiveOptions []string, mountFlags []string) error {
	mountArgs, mountArgsLogStr := MakeMountArgsSensitiveWithMountFlags(source, target, fstype, options, sensitiveOptions, mountFlags)

	// Logging with sensitive mount options removed.
	klog.V(4).Infof("Mounting cmd (%s) with arguments (%s)", mountCmd, mountArgsLogStr)
	command := exec.Command(mountCmd, mountArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		klog.Errorf("Mount failed: %v\nMounting command: %s\nMounting arguments: %s\nOutput: %s\n", err, mountCmd, mountArgsLogStr, string(output))
		return fmt.Errorf("mount failed: %v\nMounting command: %s\nMounting arguments: %s\nOutput: %s",
			err, mountCmd, mountArgsLogStr, string(output))
	}
	return err
}

// MakeMountArgsSensitive makes the arguments to the mount(8) command.
// sensitiveOptions is an extension of options except they will not be logged (because they may contain sensitive material)
func MakeMountArgsSensitive(source, target, fstype string, options []string, sensitiveOptions []string) (mountArgs []string, mountArgsLogStr string) {
	return MakeMountArgsSensitiveWithMountFlags(source, target, fstype, options, sensitiveOptions, nil /* mountFlags */)
}

// MakeMountArgsSensitiveWithMountFlags makes the arguments to the mount(8) command.
// sensitiveOptions is an extension of options except they will not be logged (because they may contain sensitive material)
// mountFlags are additional mount flags that are not related with the fstype
// and mount options
func MakeMountArgsSensitiveWithMountFlags(source, target, fstype string, options []string, sensitiveOptions []string, mountFlags []string) (mountArgs []string, mountArgsLogStr string) {
	// Build mount command as follows:
	//   mount [$mountFlags] [-t $fstype] [-o $options] [$source] $target
	mountArgs = []string{}
	mountArgsLogStr = ""

	mountArgs = append(mountArgs, mountFlags...)
	mountArgsLogStr += strings.Join(mountFlags, " ")

	if len(fstype) > 0 {
		mountArgs = append(mountArgs, "-t", fstype)
		mountArgsLogStr += strings.Join(mountArgs, " ")
	}
	if len(options) > 0 || len(sensitiveOptions) > 0 {
		combinedOptions := []string{}
		combinedOptions = append(combinedOptions, options...)
		combinedOptions = append(combinedOptions, sensitiveOptions...)
		mountArgs = append(mountArgs, "-o", strings.Join(combinedOptions, ","))
		// exclude sensitiveOptions from log string
		mountArgsLogStr += " -o " + sanitizedOptionsForLogging(options, sensitiveOptions)
	}
	if len(source) > 0 {
		mountArgs = append(mountArgs, source)
		mountArgsLogStr += " " + source
	}
	mountArgs = append(mountArgs, target)
	mountArgsLogStr += " " + target

	return mountArgs, mountArgsLogStr
}

func checkUmountError(target string, command *exec.Cmd, output []byte, err error) error {
	return fmt.Errorf("unmount failed: %v\nUnmounting arguments: %s\nOutput: %s", err, target, string(output))
}
