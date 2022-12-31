//go:build freebsd
// +build freebsd

/*
Copyright 2015 The Kubernetes Authors.

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

package emptydir

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"

	"k8s.io/api/core/v1"
)

// realMountDetector implements mountDetector in terms of syscalls.
type realMountDetector struct {
	mounter mount.Interface
}

func (m *realMountDetector) GetMountMedium(path string, requestedMedium v1.StorageMedium) (v1.StorageMedium, bool, *resource.Quantity, error) {
	klog.V(5).Infof("Determining mount medium of %v", path)
	notMnt, err := m.mounter.IsLikelyNotMountPoint(path)
	if err != nil {
		return v1.StorageMediumDefault, false, nil, fmt.Errorf("IsLikelyNotMountPoint(%q): %v", path, err)
	}

	buf := unix.Statfs_t{}
	if err := unix.Statfs(path, &buf); err != nil {
		return v1.StorageMediumDefault, false, nil, fmt.Errorf("statfs(%q): %v", path, err)
	}

	klog.V(3).Infof("Statfs_t of %v: %+v", path, buf)
	fsType := string(buf.Fstypename[:bytes.IndexByte(buf.Fstypename[:], 0)])

	if fsType == "tmpfs" {
		return v1.StorageMediumMemory, !notMnt, nil, nil
	}
	return v1.StorageMediumDefault, !notMnt, nil, nil
}
