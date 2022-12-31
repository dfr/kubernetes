//go:build !windows
// +build !windows

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

package initsystem

import (
	"fmt"
	"os/exec"
	"strings"
)

// FreeBSDInitSystem defines freebsd
type FreeBSDInitSystem struct{}

// ServiceStart tries to start a specific service
func (rcd FreeBSDInitSystem) ServiceStart(service string) error {
	args := []string{service, "start"}
	return exec.Command("service", args...).Run()
}

// ServiceStop tries to stop a specific service
func (rcd FreeBSDInitSystem) ServiceStop(service string) error {
	args := []string{service, "stop"}
	return exec.Command("service", args...).Run()
}

// ServiceRestart tries to reload the environment and restart the specific service
func (rcd FreeBSDInitSystem) ServiceRestart(service string) error {
	args := []string{service, "restart"}
	return exec.Command("service", args...).Run()
}

// ServiceExists ensures the service is defined for this init system.
// rcd writes to stderr if a service is not found or not enabled
// this is in contrast to systemd which only writes to stdout.
// Hence, we use the Combinedoutput, and ignore the error.
func (rcd FreeBSDInitSystem) ServiceExists(service string) bool {
	args := []string{service, "status"}
	outBytes, _ := exec.Command("service", args...).CombinedOutput()
	return !strings.Contains(string(outBytes), "does not exist")
}

// ServiceIsEnabled ensures the service is enabled to start on each boot.
func (rcd FreeBSDInitSystem) ServiceIsEnabled(service string) bool {
	args := []string{service, "enabled"}
	outBytes, err := exec.Command("service", args...).Output()
	return err == nil && string(outBytes) == ""
}

// ServiceIsActive ensures the service is running, or attempting to run. (crash looping in the case of kubelet)
func (rcd FreeBSDInitSystem) ServiceIsActive(service string) bool {
	args := []string{service, "status"}
	outBytes, _ := exec.Command("service", args...).CombinedOutput()
	outStr := string(outBytes)
	return !strings.Contains(outStr, "not running") && !strings.Contains(outStr, "does not exist")
}

// EnableCommand return a string describing how to enable a service
func (rcd FreeBSDInitSystem) EnableCommand(service string) string {
	return fmt.Sprintf("service %s enable", service)
}

// GetInitSystem returns an InitSystem for the current system, or nil
// if we cannot detect a supported init system.
// This indicates we will skip init system checks, not an error.
func GetInitSystem() (InitSystem, error) {
	return &FreeBSDInitSystem{}, nil
}
