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

package cadvisor

// #include <unistd.h>
// #include <sys/vmmeter.h>
// #include <sys/sysctl.h>
// #include <vm/vm_param.h>
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	//"runtime"
	"time"
	"unsafe"

	"github.com/google/cadvisor/events"
	cadvisorapi "github.com/google/cadvisor/info/v1"
	cadvisorapiv2 "github.com/google/cadvisor/info/v2"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

type cadvisorClient struct {
	imageFsInfoProvider ImageFsInfoProvider
	rootPath            string
}

var _ Interface = new(cadvisorClient)

// TODO(vmarmol): Make configurable.
// The amount of time for which to keep stats in memory.
const statsCacheDuration = 2 * time.Minute
const maxHousekeepingInterval = 15 * time.Second
const defaultHousekeepingInterval = 10 * time.Second
const allowDynamicHousekeeping = true

var startTime time.Time

func init() {
	startTime = time.Now()
}

// New creates a new cAdvisor Interface for freebsd systems.
func New(imageFsInfoProvider ImageFsInfoProvider, rootPath string, cgroupRoots []string, usingLegacyStats, localStorageCapacityIsolation bool) (Interface, error) {
	return &cadvisorClient{
		imageFsInfoProvider: imageFsInfoProvider,
		rootPath:            rootPath,
	}, nil
}

func (cc *cadvisorClient) Start() error {
	return nil
}

func (cu *cadvisorClient) DockerContainer(name string, req *cadvisorapi.ContainerInfoRequest) (cadvisorapi.ContainerInfo, error) {
	return cadvisorapi.ContainerInfo{}, nil
}

func (cc *cadvisorClient) ContainerInfo(name string, req *cadvisorapi.ContainerInfoRequest) (*cadvisorapi.ContainerInfo, error) {
	return &cadvisorapi.ContainerInfo{}, nil
}

func (cc *cadvisorClient) ContainerInfoV2(name string, options cadvisorapiv2.RequestOptions) (map[string]cadvisorapiv2.ContainerInfo, error) {
	infos := make(map[string]cadvisorapiv2.ContainerInfo)
	rootContainerInfo, err := createRootContainerInfo()
	if err != nil {
		return nil, err
	}

	infos["/"] = *rootContainerInfo

	return infos, nil
}

func (cc *cadvisorClient) VersionInfo() (*cadvisorapi.VersionInfo, error) {
	return &cadvisorapi.VersionInfo{}, nil
}

func (cc *cadvisorClient) SubcontainerInfo(name string, req *cadvisorapi.ContainerInfoRequest) (map[string]*cadvisorapi.ContainerInfo, error) {
	return map[string]*cadvisorapi.ContainerInfo{}, nil
}

func (cc *cadvisorClient) MachineInfo() (*cadvisorapi.MachineInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	memTotal, _, err := getMemInfo()
	if err != nil {
		return nil, err
	}

	return &cadvisorapi.MachineInfo{
		NumCores:         16, //runtime.NumCPU(),
		NumPhysicalCores: 8,
		NumSockets:       1,
		MemoryCapacity:   uint64(memTotal),
		MachineID:        hostname,
	}, nil
}

func (cc *cadvisorClient) ImagesFsInfo() (cadvisorapiv2.FsInfo, error) {
	// TODO: don't hardcode the path here
	return cc.GetDirFsInfo("/var/db/containers")
}

func (cc *cadvisorClient) RootFsInfo() (cadvisorapiv2.FsInfo, error) {
	return cc.GetDirFsInfo(cc.rootPath)
}

func (cc *cadvisorClient) WatchEvents(request *events.Request) (*events.EventChannel, error) {
	return &events.EventChannel{}, nil
}

func toString(bs []byte) string {
	return string(bs[:bytes.IndexByte(bs, 0)])
}

func (cu *cadvisorClient) GetDirFsInfo(path string) (cadvisorapiv2.FsInfo, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return cadvisorapiv2.FsInfo{}, err
	}

	return cadvisorapiv2.FsInfo{
		Device:     toString(stat.Mntfromname[:]),
		Mountpoint: toString(stat.Mntonname[:]),
		Capacity:   stat.Blocks * stat.Bsize,
		Available:  uint64(stat.Bavail) * stat.Bsize,
		Usage:      (stat.Blocks - uint64(stat.Bavail)) * stat.Bsize,
		Inodes:     nil,
		InodesFree: nil,
	}, nil
}

func (cu *cadvisorClient) GetRequestedContainersInfo(containerName string, options cadvisorapiv2.RequestOptions) (map[string]*cadvisorapi.ContainerInfo, error) {
	return nil, nil
}

func getMemInfo() (int64, int64, error) {
	data, err := unix.SysctlRaw("vm.vmtotal")
	if err != nil {
		return -1, -1, fmt.Errorf("can't get kernel info: %w", err)
	}
	if len(data) != C.sizeof_struct_vmtotal {
		return -1, -1, fmt.Errorf("unexpected vmtotal size %d", len(data))
	}

	total := (*C.struct_vmtotal)(unsafe.Pointer(&data[0]))

	pagesize := int64(C.sysconf(C._SC_PAGESIZE))
	npages := int64(C.sysconf(C._SC_PHYS_PAGES))
	return pagesize * npages, pagesize * int64(total.t_free), nil
}

func createRootContainerInfo() (*cadvisorapiv2.ContainerInfo, error) {
	// Jail id zero represents the host.
	entries, err := getRacct("jail:0")
	if err != nil {
		return nil, err
	}
	cpu, err := getCPUTime()

	physmem, err := unix.SysctlUint64("hw.physmem")
	if err != nil {
		return nil, fmt.Errorf("can't get hw.physmem value: %w", err)
	}

	stats := &cadvisorapiv2.ContainerStats{Timestamp: time.Now()}
	userTime := cpu.user + cpu.nice
	sysTime := cpu.sys + cpu.intr
	stats.Cpu = &cadvisorapi.CpuStats{
		Usage: cadvisorapi.CpuUsage{
			Total:  uint64((userTime + sysTime) * 1000000000),
			User:   uint64(userTime * 1000000000),
			System: uint64(sysTime * 1000000000),
		},
	}
	if val, ok := entries["pcpu"]; ok {
		stats.CpuInst = &cadvisorapiv2.CpuInstStats{
			Usage: cadvisorapiv2.CpuInstUsage{
				Total: (val / 100.0) * 1000000000,
			},
		}
	}
	if val, ok := entries["memoryuse"]; ok {
		stats.Memory = &cadvisorapi.MemoryStats{
			WorkingSet: val,
		}
	}
	// TODO: network stats?

	rootInfo := cadvisorapiv2.ContainerInfo{
		Spec: cadvisorapiv2.ContainerSpec{
			CreationTime: startTime,
			HasCpu:       true,
			HasMemory:    true,
			HasNetwork:   false,
			Memory: cadvisorapiv2.MemorySpec{
				Limit: physmem,
			},
		},
		Stats: []*cadvisorapiv2.ContainerStats{stats},
	}

	return &rootInfo, nil
}

// TODO: factor this out and add the prometheus copyright

type clockinfo struct {
	hz     int32 // clock frequency
	tick   int32 // micro-seconds per hz tick
	spare  int32
	stathz int32 // statistics clock frequency
	profhz int32 // profiling clock frequency
}

type cputime struct {
	user float64
	nice float64
	sys  float64
	intr float64
	idle float64
}

func getCPUTime() (*cputime, error) {
	const states = 5

	clockb, err := unix.SysctlRaw("kern.clockrate")
	if err != nil {
		return nil, err
	}
	clock := *(*clockinfo)(unsafe.Pointer(&clockb[0]))
	cpb, err := unix.SysctlRaw("kern.cp_time")
	if err != nil {
		return nil, err
	}

	var cpufreq float64
	if clock.stathz > 0 {
		cpufreq = float64(clock.stathz)
	} else {
		cpufreq = float64(clock.hz)
	}
	var times []float64
	for len(cpb) >= int(unsafe.Sizeof(int(0))) {
		t := *(*int)(unsafe.Pointer(&cpb[0]))
		times = append(times, float64(t)/cpufreq)
		cpb = cpb[unsafe.Sizeof(int(0)):]
	}

	cpu := &cputime{
		user: times[0],
		nice: times[0],
		sys:  times[0],
		intr: times[0],
		idle: times[0],
	}

	return cpu, nil
}

func getRacct(filter string) (map[string]uint64, error) {
	bp, err := syscall.ByteSliceFromString(filter)
	if err != nil {
		return nil, err
	}
	var buf [1024]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_RCTL_GET_RACCT,
		uintptr(unsafe.Pointer(&bp[0])),
		uintptr(len(bp)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("error calling rctl_get_racct with filter %s: %v", errno)
	}
	len := bytes.IndexByte(buf[:], byte(0))
	entries := strings.Split(string(buf[:len]), ",")
	res := make(map[string]uint64)
	for _, entry := range entries {
		kv := strings.SplitN(entry, "=", 2)
		key := kv[0]
		val, err := strconv.ParseUint(kv[1], 10, 0)
		if err != nil {
			klog.Warningf("unexpected rctl entry, ignoring: %s", entry)
		}
		res[key] = val
	}
	return res, nil
}
