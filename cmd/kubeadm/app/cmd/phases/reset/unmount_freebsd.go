//go:build freebsd

/*
Copyright 2019 The Kubernetes Authors.

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

package phases

import (
	"bytes"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"k8s.io/klog/v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/errors"
)

var flagMap = map[string]int{
	kubeadmapi.UnmountFlagMNTForce:       unix.MNT_FORCE,
	kubeadmapi.UnmountFlagMNTDetach:      unix.MNT_FORCE,
	kubeadmapi.UnmountFlagMNTExpire:      0,
	kubeadmapi.UnmountFlagUmountNoFollow: 0,
}

func flagsToInt(flags []string) int {
	res := 0
	for _, f := range flags {
		res |= flagMap[f]
	}
	return res
}

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

// unmountKubeletDirectory unmounts all paths that contain KubeletRunDirectory
func unmountKubeletDirectory(kubeletRunDirectory string, flags []string) error {
	mounts, err := getMounts()
	if err != nil {
		return err
	}

	if !strings.HasSuffix(kubeletRunDirectory, "/") {
		// trailing "/" is needed to ensure that possibly mounted /var/lib/kubelet is skipped
		kubeletRunDirectory += "/"
	}

	var errList []error
	flagsInt := flagsToInt(flags)
	for _, mount := range mounts {
		mountPoint := string(mount.Mntonname[:bytes.IndexByte(mount.Mntonname[:], 0)])
		if !strings.HasPrefix(mountPoint, kubeletRunDirectory) {
			continue
		}
		klog.V(5).Infof("[reset] Unmounting %q", mountPoint)
		if err := syscall.Unmount(mountPoint, flagsInt); err != nil {
			errList = append(errList, errors.WithMessagef(err, "failed to unmount %q", mountPoint))
		}
	}
	return errors.Wrapf(utilerrors.NewAggregate(errList),
		"encountered the following errors while unmounting directories in %q", kubeletRunDirectory)
}
