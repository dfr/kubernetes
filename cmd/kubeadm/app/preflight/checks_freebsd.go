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

package preflight

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"k8s.io/klog/v2"
	system "k8s.io/system-validators/validators"
	utilsexec "k8s.io/utils/exec"

	"k8s.io/kubernetes/cmd/kubeadm/app/util/errors"
)

const (
	bridgepf      = "net.link.bridge.pfil_member"
	ipforwarding  = "net.inet.ip.forwarding"
	ip6forwarding = "net.inet6.ip6.forwarding"
	pffilter      = "net.pf.filter_local"
)

// Check number of memory required by kubeadm
func (mc MemCheck) Check() (warnings, errorList []error) {
	physmem, err := unix.SysctlUint64("hw.physmem")
	if err != nil {
		errorList = append(errorList, errors.Wrapf(err, "can't get hw.physmem value: %w"))
	}

	// Physmem holds the total usable memory in bytes.
	actual := physmem / 1024 / 1024
	if actual < mc.Mem {
		errorList = append(errorList, errors.Errorf("the system RAM (%d MB) is less than the minimum %d MB", actual, mc.Mem))
	}
	return warnings, errorList
}

// SysctlCheck checks that the given file contains the string Content.
type SysctlCheck struct {
	Path    string
	Content string
	Label   string
}

// Name returns label for individual SysctlChecks. If not known, will return based on path.
func (scc SysctlCheck) Name() string {
	if scc.Label != "" {
		return scc.Label
	}
	return fmt.Sprintf("Sysctl-%s", scc.Path)
}

// Check validates if the given file contains the given content.
func (scc SysctlCheck) Check() (warnings, errorList []error) {
	klog.V(1).Infof("validating the value of sysctl %s", scc.Path)
	cmd := utilsexec.New().Command("sysctl", "-n", scc.Path)
	buf, err := cmd.CombinedOutput()
	if err != nil {
		return nil, []error{errors.Errorf("%s could not be read", scc.Path)}
	}

	val := strings.TrimSpace(string(buf))
	if val != scc.Content {
		return nil, []error{errors.Errorf("%s value is not %s", scc.Path, scc.Content)}
	}
	return nil, []error{}

}

// addOSValidator adds a new OSValidator
func addOSValidator(validators []system.Validator, reporter *system.StreamReporter, _ string) []system.Validator {
	validators = append(validators, &system.OSValidator{Reporter: reporter}, &system.CgroupsValidator{Reporter: reporter})
	return validators
}

// addIPv6Checks adds IPv6 related bridgenf and forwarding checks
func addIPv6Checks(checks []Checker) []Checker {
	checks = append(checks,
		SysctlCheck{Path: bridgepf, Content: "1"},
		SysctlCheck{Path: ip6forwarding, Content: "1"},
		SysctlCheck{Path: pffilter, Content: "1"},
	)
	return checks
}

// addIPv4Checks adds IPv4 related bridgenf and forwarding checks
func addIPv4Checks(checks []Checker) []Checker {
	checks = append(checks,
		SysctlCheck{Path: bridgepf, Content: "1"},
		SysctlCheck{Path: ipforwarding, Content: "1"},
		SysctlCheck{Path: pffilter, Content: "1"},
	)
	return checks
}

// addSwapCheck adds a swap check
func addSwapCheck(checks []Checker) []Checker {
	checks = append(checks, SwapCheck{})
	return checks
}

// addExecChecks adds checks that verify if certain binaries are in PATH
func addExecChecks(checks []Checker, execer utilsexec.Interface, _ string) []Checker {
	checks = append(checks,
		InPathCheck{executable: "crictl", mandatory: true, exec: execer},
		InPathCheck{executable: "ifconfig", mandatory: true, exec: execer},
		InPathCheck{executable: "pfctl", mandatory: true, exec: execer},
		InPathCheck{executable: "mount", mandatory: true, exec: execer},
		InPathCheck{executable: "touch", mandatory: false, exec: execer})
	return checks
}
