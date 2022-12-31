//go:build freebsd
// +build freebsd

/*
Copyright 2017 The Kubernetes Authors.

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

package system

import (
	"os/exec"
	"strings"
)

// DefaultSysSpec is the default SysSpec for Linux
var DefaultSysSpec = SysSpec{
	OS: "FreeBSD",
	KernelSpec: KernelSpec{
		Versions:  []string{`^1[4-9]\.[0-9]-.*$`, `^[2-9][0-9]\.[0-9]-.*$`}, // Requires 14.0 or newer
		Required:  []KernelConfig{},
		Optional:  []KernelConfig{},
		Forbidden: []KernelConfig{},
	},
	RuntimeSpec: RuntimeSpec{
		DockerSpec: &DockerSpec{
			Version:     []string{`1\.1[1-3]\..*`, `17\.0[3,6,9]\..*`, `18\.0[6,9]\..*`, `19\.03\..*`, `20\.10\..*`},
			GraphDriver: []string{"zfs"},
		},
	},
}

// KernelValidatorHelperImpl is the 'linux' implementation of KernelValidatorHelper
type KernelValidatorHelperImpl struct{}

var _ KernelValidatorHelper = &KernelValidatorHelperImpl{}

// GetKernelReleaseVersion returns the kernel release version (ex. 4.4.0-96-generic) as a string
func (o *KernelValidatorHelperImpl) GetKernelReleaseVersion() (string, error) {
	releaseVersion, err := exec.Command("uname", "-r").CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(releaseVersion)), nil
}
