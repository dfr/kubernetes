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

package pf

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/proxy"
	"k8s.io/kubernetes/pkg/proxy/healthcheck"
	"k8s.io/kubernetes/pkg/proxy/metaproxier"
	"k8s.io/kubernetes/pkg/proxy/metrics"
	proxyutil "k8s.io/kubernetes/pkg/proxy/util"
	"k8s.io/kubernetes/pkg/util/async"
	utilexec "k8s.io/utils/exec"
	netutils "k8s.io/utils/net"
)

// internal struct for string service information
type servicePortInfo struct {
	*proxy.BaseServicePortInfo
	// The following fields are computed and stored for performance reasons.
	nameString string
	anchorName string
}

// returns a new proxy.ServicePort which abstracts a serviceInfo
func newServiceInfo(port *v1.ServicePort, service *v1.Service, bsvcPortInfo *proxy.BaseServicePortInfo, ipFamily v1.IPFamily) proxy.ServicePort {
	svcPort := &servicePortInfo{BaseServicePortInfo: bsvcPortInfo}

	// Store the following for performance reasons.
	svcName := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
	svcPortName := proxy.ServicePortName{NamespacedName: svcName, Port: port.Name}
	protocol := strings.ToLower(string(svcPort.Protocol()))
	svcPort.nameString = svcPortName.String()
	svcPort.anchorName = anchorName(svcPort.nameString, protocol, ipFamily)

	return svcPort
}

// internal struct for endpoints information
type endpointsInfo struct {
	*proxy.BaseEndpointInfo
}

// returns a new proxy.Endpoint which abstracts a endpointsInfo
func newEndpointInfo(baseInfo *proxy.BaseEndpointInfo, svcPortName *proxy.ServicePortName) proxy.Endpoint {
	return &endpointsInfo{
		BaseEndpointInfo: baseInfo,
	}
}

// Equal overrides the Equal() function implemented by proxy.BaseEndpointInfo.
func (e *endpointsInfo) Equal(other proxy.Endpoint) bool {
	o, ok := other.(*endpointsInfo)
	if !ok {
		klog.ErrorS(nil, "Failed to cast endpointsInfo")
		return false
	}
	return e.Endpoint == o.Endpoint &&
		e.IsLocal == o.IsLocal &&
		e.Ready == o.Ready
}

type LocalTrafficDetector interface {
	// IsImplemented returns true if the implementation does something, false otherwise
	IsImplemented() bool

	// IfLocal returns pf expression that will match traffic from a pod
	IfLocal() string

	// IfNotLocal returns pf expression that will match traffic that is not from a pod
	IfNotLocal() string

	// Address expression to translate non-local source address
	ToAddress() string
}

// Proxier is an pf based proxy for connections between a localhost:lport
// and services that provide the actual backends.
type Proxier struct {
	// Address family for this proxier
	ipFamily v1.IPFamily

	// endpointsChanges and serviceChanges contains all changes to endpoints
	// and services that happened since pf was synced. For a single object,
	// changes are accumulated, i.e. previous is state from before all of
	// them, current is state after applying all of those.
	endpointsChanges *proxy.EndpointsChangeTracker
	serviceChanges   *proxy.ServiceChangeTracker

	mu           sync.Mutex // protects the following fields
	svcPortMap   proxy.ServicePortMap
	endpointsMap proxy.EndpointsMap
	nodeLabels   map[string]string
	// endpointSlicesSynced, and servicesSynced are set to true when
	// corresponding objects are synced after startup. This is used to avoid
	// updating pf with some partial data after kube-proxy restart.
	endpointSlicesSynced bool
	servicesSynced       bool
	initialized          int32
	syncRunner           *async.BoundedFrequencyRunner // governs calls to syncProxyRules
	syncPeriod           time.Duration

	// These are effectively const and do not need the mutex to be held.
	exec          utilexec.Interface
	localDetector LocalTrafficDetector
	hostname      string
	nodeIP        net.IP
	recorder      events.EventRecorder

	serviceHealthServer healthcheck.ServiceHealthServer
	healthzServer       *healthcheck.ProxierHealthServer

	// The following buffers are used to reuse memory and avoid allocations
	// that are significantly impacting performance.
	pfData proxyutil.LineBuffer

	// Values are as a parameter to select the interfaces where nodePort works.
	nodePortAddresses *proxyutil.NodePortAddresses
	// networkInterfacer defines an interface for several net library functions.
	// Inject for test purpose.
	networkInterfacer proxyutil.NetworkInterfacer
}

// Proxier implements proxy.Provider
var _ proxy.Provider = &Proxier{}

// NewProxier returns a new Proxier for PF
func NewProxier(ipFamily v1.IPFamily,
	exec utilexec.Interface,
	syncPeriod time.Duration,
	minSyncPeriod time.Duration,
	localDetector LocalTrafficDetector,
	hostname string,
	nodeIP net.IP,
	recorder events.EventRecorder,
	healthzServer *healthcheck.ProxierHealthServer,
	nodePortAddressStrings []string,
) (*Proxier, error) {
	nodePortAddresses := proxyutil.NewNodePortAddresses(ipFamily, nodePortAddressStrings)

	serviceHealthServer := healthcheck.NewServiceHealthServer(hostname, recorder, nodePortAddresses, healthzServer)

	newServiceInfoWrapper := func(port *v1.ServicePort, service *v1.Service, bsvcPortInfo *proxy.BaseServicePortInfo) proxy.ServicePort {
		return newServiceInfo(port, service, bsvcPortInfo, ipFamily)
	}
	proxier := &Proxier{
		ipFamily:            ipFamily,
		svcPortMap:          make(proxy.ServicePortMap),
		serviceChanges:      proxy.NewServiceChangeTracker(newServiceInfoWrapper, ipFamily, recorder, nil),
		endpointsMap:        make(proxy.EndpointsMap),
		endpointsChanges:    proxy.NewEndpointsChangeTracker(hostname, newEndpointInfo, ipFamily, recorder, nil),
		exec:                exec,
		localDetector:       localDetector,
		hostname:            hostname,
		nodeIP:              nodeIP,
		recorder:            recorder,
		serviceHealthServer: serviceHealthServer,
		healthzServer:       healthzServer,
		pfData:              proxyutil.NewLineBuffer(),
		nodePortAddresses:   nodePortAddresses,
		networkInterfacer:   proxyutil.RealNetwork{},
	}

	burstSyncs := 2
	klog.V(3).InfoS("Record sync param", "minSyncPeriod", minSyncPeriod, "syncPeriod", syncPeriod, "burstSyncs", burstSyncs)
	proxier.syncRunner = async.NewBoundedFrequencyRunner("sync-runner", proxier.syncProxyRules, minSyncPeriod, syncPeriod, burstSyncs)

	return proxier, nil
}

// NewDualStackProxier creates a MetaProxier instance, with IPv4 and IPv6 proxies.
func NewDualStackProxier(
	exec utilexec.Interface,
	syncPeriod time.Duration,
	minSyncPeriod time.Duration,
	localDetectors [2]LocalTrafficDetector,
	hostname string,
	nodeIPs map[v1.IPFamily]net.IP,
	recorder events.EventRecorder,
	healthzServer *healthcheck.ProxierHealthServer,
	nodePortAddresses []string,
) (proxy.Provider, error) {
	ipv4Proxier, err := NewProxier(
		v1.IPv4Protocol,
		exec,
		syncPeriod,
		minSyncPeriod,
		localDetectors[0],
		hostname,
		nodeIPs[v1.IPv4Protocol],
		recorder,
		healthzServer,
		nodePortAddresses,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create ipv4 proxier: %v", err)
	}
	ipv6Proxier, err := NewProxier(
		v1.IPv6Protocol,
		exec,
		syncPeriod,
		minSyncPeriod,
		localDetectors[1],
		hostname,
		nodeIPs[v1.IPv4Protocol],
		recorder,
		healthzServer,
		nodePortAddresses,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create ipv6 proxier: %v", err)
	}
	return metaproxier.NewMetaProxier(ipv4Proxier, ipv6Proxier), nil
}

// CleanupLeftovers removes all pf rules created by the Proxier It returns true
// if an error was encountered. Errors are logged.
func CleanupLeftovers(exec utilexec.Interface) (encounteredError bool) {
	klog.V(2).Infof("listing PF anchors")
	cmd := exec.Command("pfctl", "-a", "cni-rdr", "-s", "Anchors")
	out, err := cmd.Output()
	if err != nil {
		klog.Errorf("listing PF anchors: %v", err)
		return true
	}

	encounteredError = false
	anchors := strings.Split(string(out), "\n")
	for _, anchor := range anchors {
		anchor = strings.TrimSpace(anchor)
		if strings.HasPrefix(anchor, "cni-rdr/kube-svc-") {
			klog.V(2).Infof("deleting PF anchor %s", anchor)
			cmd := exec.Command("pfctl", "-a", anchor, "-F", "nat")
			err := cmd.Run()
			if err != nil {
				klog.Errorf("deleting anchor %s: %v", anchor, err)
				encounteredError = true
			}
		}
	}
	return encounteredError
}

// Sync is called to synchronize the proxier state to pf as soon as possible.
func (proxier *Proxier) Sync() {
	if proxier.healthzServer != nil {
		proxier.healthzServer.QueuedUpdate(proxier.ipFamily)
	}
	metrics.SyncProxyRulesLastQueuedTimestamp.SetToCurrentTime()
	proxier.syncRunner.Run()
}

// SyncLoop runs periodic work.  This is expected to run as a goroutine or as the main loop of the app.  It does not return.
func (proxier *Proxier) SyncLoop() {
	// Update healthz timestamp at beginning in case Sync() never succeeds.
	if proxier.healthzServer != nil {
		proxier.healthzServer.Updated(proxier.ipFamily)
	}

	// synthesize "last change queued" time as the informers are syncing.
	metrics.SyncProxyRulesLastQueuedTimestamp.SetToCurrentTime()
	proxier.syncRunner.Loop(wait.NeverStop)
}

func (proxier *Proxier) setInitialized(value bool) {
	var initialized int32
	if value {
		initialized = 1
	}
	atomic.StoreInt32(&proxier.initialized, initialized)
}

func (proxier *Proxier) isInitialized() bool {
	return atomic.LoadInt32(&proxier.initialized) > 0
}

// OnServiceAdd is called whenever creation of new service object
// is observed.
func (proxier *Proxier) OnServiceAdd(service *v1.Service) {
	proxier.OnServiceUpdate(nil, service)
}

// OnServiceUpdate is called whenever modification of an existing
// service object is observed.
func (proxier *Proxier) OnServiceUpdate(oldService, service *v1.Service) {
	if proxier.serviceChanges.Update(oldService, service) && proxier.isInitialized() {
		proxier.Sync()
	}
}

// OnServiceDelete is called whenever deletion of an existing service
// object is observed.
func (proxier *Proxier) OnServiceDelete(service *v1.Service) {
	proxier.OnServiceUpdate(service, nil)

}

// OnServiceSynced is called once all the initial event handlers were
// called and the state is fully propagated to local cache.
func (proxier *Proxier) OnServiceSynced() {
	proxier.mu.Lock()
	proxier.servicesSynced = true
	proxier.setInitialized(proxier.endpointSlicesSynced)
	proxier.mu.Unlock()

	// Sync unconditionally - this is called once per lifetime.
	proxier.syncProxyRules()
}

// OnEndpointSliceAdd is called whenever creation of a new endpoint slice object
// is observed.
func (proxier *Proxier) OnEndpointSliceAdd(endpointSlice *discovery.EndpointSlice) {
	if proxier.endpointsChanges.EndpointSliceUpdate(endpointSlice, false) && proxier.isInitialized() {
		proxier.Sync()
	}
}

// OnEndpointSliceUpdate is called whenever modification of an existing endpoint
// slice object is observed.
func (proxier *Proxier) OnEndpointSliceUpdate(_, endpointSlice *discovery.EndpointSlice) {
	if proxier.endpointsChanges.EndpointSliceUpdate(endpointSlice, false) && proxier.isInitialized() {
		proxier.Sync()
	}
}

// OnEndpointSliceDelete is called whenever deletion of an existing endpoint slice
// object is observed.
func (proxier *Proxier) OnEndpointSliceDelete(endpointSlice *discovery.EndpointSlice) {
	if proxier.endpointsChanges.EndpointSliceUpdate(endpointSlice, true) && proxier.isInitialized() {
		proxier.Sync()
	}
}

// OnEndpointSlicesSynced is called once all the initial event handlers were
// called and the state is fully propagated to local cache.
func (proxier *Proxier) OnEndpointSlicesSynced() {
	proxier.mu.Lock()
	proxier.endpointSlicesSynced = true
	proxier.setInitialized(proxier.servicesSynced)
	proxier.mu.Unlock()

	// Sync unconditionally - this is called once per lifetime.
	proxier.syncProxyRules()
}

// OnNodeAdd is called whenever creation of new node object
// is observed.
func (proxier *Proxier) OnNodeAdd(node *v1.Node) {
	if node.Name != proxier.hostname {
		klog.ErrorS(nil, "Received a watch event for a node that doesn't match the current node",
			"eventNode", node.Name, "currentNode", proxier.hostname)
		return
	}

	if reflect.DeepEqual(proxier.nodeLabels, node.Labels) {
		return
	}

	proxier.mu.Lock()
	proxier.nodeLabels = map[string]string{}
	for k, v := range node.Labels {
		proxier.nodeLabels[k] = v
	}
	proxier.mu.Unlock()
	klog.V(4).InfoS("Updated proxier node labels", "labels", node.Labels)

	proxier.Sync()
}

// OnNodeUpdate is called whenever modification of an existing
// node object is observed.
func (proxier *Proxier) OnNodeUpdate(oldNode, node *v1.Node) {
	if node.Name != proxier.hostname {
		klog.ErrorS(nil, "Received a watch event for a node that doesn't match the current node",
			"eventNode", node.Name, "currentNode", proxier.hostname)
		return
	}

	if reflect.DeepEqual(proxier.nodeLabels, node.Labels) {
		return
	}

	proxier.mu.Lock()
	proxier.nodeLabels = map[string]string{}
	for k, v := range node.Labels {
		proxier.nodeLabels[k] = v
	}
	proxier.mu.Unlock()
	klog.V(4).InfoS("Updated proxier node labels", "labels", node.Labels)

	proxier.Sync()
}

// OnNodeDelete is called whenever deletion of an existing node
// object is observed.
func (proxier *Proxier) OnNodeDelete(node *v1.Node) {
	if node.Name != proxier.hostname {
		klog.ErrorS(nil, "Received a watch event for a node that doesn't match the current node",
			"eventNode", node.Name, "currentNode", proxier.hostname)
		return
	}
	proxier.mu.Lock()
	proxier.nodeLabels = nil
	proxier.mu.Unlock()

	proxier.Sync()
}

// OnNodeSynced is called once all the initial event handlers were
// called and the state is fully propagated to local cache.
func (proxier *Proxier) OnNodeSynced() {
}

// portProtoHash takes the ServicePortName and protocol for a service returns
// the associated 16 character hash. This is computed by hashing (sha256) then
// encoding to base32 and truncating to 16 chars. We do this because PF anchor
// names must be <= 64 chars long, and the longer they are the harder they are
// to read.
func portProtoHash(servicePortName string, protocol string) string {
	hash := sha256.Sum256([]byte(servicePortName + protocol))
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	return encoded[:16]
}

// Construct an anchor name based on the ServicePortName and protocol
func anchorName(servicePortName string, protocol string, ipFamily v1.IPFamily) string {
	af := "v4"
	if ipFamily == v1.IPv6Protocol {
		af = "v6"
	}
	return fmt.Sprintf("kube-svc-%s-%s", af, portProtoHash(servicePortName, protocol))
}

func (proxier *Proxier) redirectRule(af, proto string, destinationIP net.IP, destinationPort int, targetIPs string, targetPort int) {
	proxier.pfData.Write(
		"rdr", af, "proto", proto,
		"from any to", destinationIP.String(),
		"port", strconv.Itoa(destinationPort),
		"->", targetIPs, "port", strconv.Itoa(targetPort),
	)
}

func (proxier *Proxier) natRule(af, proto string, targetIPs string, targetPort int) {
	proxier.pfData.Write(
		"nat", af, "proto", proto,
		"from", proxier.localDetector.IfNotLocal(),
		"to", targetIPs,
		"port", strconv.Itoa(targetPort),
		"->", proxier.localDetector.ToAddress(),
	)
}

// This is where all of the iptables-save/restore calls happen.
// The only other iptables rules are those that are setup in iptablesInit()
// This assumes proxier.mu is NOT held
func (proxier *Proxier) syncProxyRules() {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()

	// don't sync rules till we've received services and endpoints
	if !proxier.isInitialized() {
		klog.V(2).InfoS("Not syncing pf until Services and Endpoints have been received from master")
		return
	}

	// Keep track of how long syncs take.
	start := time.Now()
	defer func() {
		metrics.SyncProxyRulesLatency.Observe(metrics.SinceInSeconds(start))
		klog.V(2).InfoS("SyncProxyRules complete", "elapsed", time.Since(start))
	}()

	// Get a pre-update list of anchors - we will remove entries from this
	// set for each active anchor and what remains can be removed.
	staleAnchors := sets.NewString()
	for _, svc := range proxier.svcPortMap {
		if svcInfo, ok := svc.(*servicePortInfo); ok {
			staleAnchors.Insert(svcInfo.anchorName)
		}
	}

	// TODO: figure out if partial sync makes sense. For now, just re-write all the rules
	proxier.svcPortMap.Update(proxier.serviceChanges)
	proxier.endpointsMap.Update(proxier.endpointsChanges)

	// TODO: figure out how to delete anchors for stale services

	klog.V(2).InfoS("Syncing pf rules")

	//
	// Below this point we will not return until we try to write the pf rules.
	nodeIPs, err := proxier.nodePortAddresses.GetNodeIPs(proxier.networkInterfacer)
	if err != nil {
		klog.ErrorS(err, "Failed to get node ip address matching nodeport cidrs, services with nodeport may not work as intended", "CIDRs", proxier.nodePortAddresses)
	}

	// Build rules for each service-port.
	for svcName, svc := range proxier.svcPortMap {
		svcInfo, ok := svc.(*servicePortInfo)
		if !ok {
			klog.ErrorS(nil, "Failed to cast serviceInfo", "serviceName", svcName)
			continue
		}
		staleAnchors.Delete(svcInfo.anchorName)
		//protocol := strings.ToLower(string(svcInfo.Protocol()))
		//svcPortNameString := svcInfo.nameString

		// Figure out the endpoints for Cluster and Local traffic policy.
		// allLocallyReachableEndpoints is the set of all endpoints that can be routed to
		// from this node, given the service's traffic policies. hasEndpoints is true
		// if the service has any usable endpoints on any node, not just this one.
		allEndpoints := proxier.endpointsMap[svcName]
		clusterEndpoints, localEndpoints, allLocallyReachableEndpoints, _ := proxy.CategorizeEndpoints(allEndpoints, svcInfo, proxier.nodeLabels)
		if klog.V(2).Enabled() {
			klog.Infof("Service %s:", svcName)
			klog.Infof("clusterEndpoints: %v", clusterEndpoints)
			klog.Infof("localEndpoints: %v", localEndpoints)
			klog.Infof("allLocallyReachableEndpoints: %v", allLocallyReachableEndpoints)
		}

		// Note: we can only redirect traffic to a single port with
		// PF. We choose the largest group here - external load
		// balancing would be needede to spread traffic across multiple
		// ports.
		endpointsByPort := map[int][]proxy.Endpoint{}
		for _, ep := range allLocallyReachableEndpoints {
			if port, err := ep.Port(); err == nil {
				endpointsByPort[port] = append(endpointsByPort[port], ep)
			}
		}
		targetEndpoints := []proxy.Endpoint{}
		targetPort := 0
		for port, eps := range endpointsByPort {
			if len(eps) > len(targetEndpoints) {
				targetEndpoints = eps
				targetPort = port
			}
		}
		if len(targetEndpoints) == 0 {
			continue
		}

		// Format this list for the redirect rule(s)
		ips := []string{}
		for _, ep := range targetEndpoints {
			ips = append(ips, ep.IP())
		}
		redirHosts := "{ " + strings.Join(ips, ", ") + " }"

		af := "inet"
		if proxier.ipFamily == v1.IPv6Protocol {
			af = "inet6"
		}

		var proto string
		switch svcInfo.Protocol() {
		case v1.ProtocolTCP:
			proto = "tcp"
		case v1.ProtocolUDP:
			proto = "udp"
		case v1.ProtocolSCTP:
			proto = "sctp"
		}

		// We need to redirect the service ip+port and optionally also
		// the node port address+port
		proxier.pfData.Reset()
		proxier.redirectRule(af, proto, svcInfo.ClusterIP(), svcInfo.Port(), redirHosts, targetPort)
		if svcInfo.NodePort() != 0 {
			for _, ip := range nodeIPs {
				// Don't try to redirect ::1/128 or 127.0.0.0/8
				if ip.IsLoopback() {
					continue
				}
				proxier.redirectRule(af, proto, ip, svcInfo.NodePort(), redirHosts, targetPort)
			}
			proxier.natRule(af, proto, redirHosts, targetPort)
		}

		if klog.V(2).Enabled() {
			klog.Infof("pfctl -a cni-rdr/%s -f - <EOF", svcInfo.anchorName)
			tmp := strings.Split(proxier.pfData.String(), "\n")
			for _, r := range tmp[:len(tmp)-1] {
				klog.Infof("%s", r)
			}
			klog.Infof("EOF")
		}
		input := proxier.pfData.Bytes()
		cmd := proxier.exec.Command("pfctl", "-a", "cni-rdr/"+svcInfo.anchorName, "-f", "-")
		cmd.SetStdin(bytes.NewReader(input))
		output, err := cmd.CombinedOutput()
		if err != nil {
			klog.Errorf("error loading rules input: '%s', output: %s, err: %v", input, output, err)
		}
	}
	for a := range staleAnchors {
		klog.V(2).Infof("deleting PF anchor %s", a)
		cmd := proxier.exec.Command("pfctl", "-a", "cni-rdr/"+a, "-F", "nat")
		err := cmd.Run()
		if err != nil {
			klog.Errorf("error flushing rules, err: %v", err)
		}
	}
}

func (proxier *Proxier) writeServiceToEndpointRules(svcPortNameString string, svcInfo proxy.ServicePort, endpoints []proxy.Endpoint, args []string) {
	// First write session affinity rules, if applicable.
	if svcInfo.SessionAffinityType() == v1.ServiceAffinityClientIP {
		for _, ep := range endpoints {
			epInfo, ok := ep.(*endpointsInfo)
			if !ok {
				continue
			}
			klog.Infof(`affinity: "%s -> %s"`, svcPortNameString, epInfo.Endpoint)
		}
	}

	// Now write loadbalancing rules.
	for _, ep := range endpoints {
		epInfo, ok := ep.(*endpointsInfo)
		if !ok {
			continue
		}
		klog.Infof(`loadbalance: "%s -> %s"`, svcPortNameString, epInfo.Endpoint)
	}
}

type noOpLocalDetector struct{}

// NewNoOpLocalDetector is a no-op implementation of LocalTrafficDetector
func NewNoOpLocalDetector() LocalTrafficDetector {
	return &noOpLocalDetector{}
}

func (n *noOpLocalDetector) IsImplemented() bool {
	return false
}

func (n *noOpLocalDetector) IfLocal() string {
	return "any" // no-op; matches all traffic
}

func (n *noOpLocalDetector) IfNotLocal() string {
	return "any" // no-op; matches all traffic
}

func (n *noOpLocalDetector) ToAddress() string {
	return ""
}

type detectLocalByCIDR struct {
	ifLocal    string
	ifNotLocal string
	toAddress  string
}

// NewDetectLocalByCIDR implements the LocalTrafficDetector interface using a CIDR. This can be used when a single CIDR
// range can be used to capture the notion of local traffic.
func NewDetectLocalByCIDR(cidr string) (LocalTrafficDetector, error) {
	_, cidrNet, err := netutils.ParseCIDRSloppy(cidr)
	if err != nil {
		return nil, err
	}
	cidrIp, err := netutils.GetIndexedIP(cidrNet, 1)
	if err != nil {
		return nil, err
	}

	return &detectLocalByCIDR{
		ifLocal:    cidr,
		ifNotLocal: "! " + cidr,
		toAddress:  cidrIp.String(),
	}, nil
}

func (d *detectLocalByCIDR) IsImplemented() bool {
	return true
}

func (d *detectLocalByCIDR) IfLocal() string {
	return d.ifLocal
}

func (d *detectLocalByCIDR) IfNotLocal() string {
	return d.ifNotLocal
}

func (d *detectLocalByCIDR) ToAddress() string {
	return d.toAddress
}

type detectLocalByBridgeInterface struct {
	ifLocal    string
	ifNotLocal string
	toAddress  string
}

// NewDetectLocalByBridgeInterface implements the LocalTrafficDetector interface using a bridge interface name.
// This can be used when a bridge can be used to capture the notion of local traffic from pods.
func NewDetectLocalByBridgeInterface(interfaceName string) (LocalTrafficDetector, error) {
	if len(interfaceName) == 0 {
		return nil, fmt.Errorf("no bridge interface name set")
	}
	return &detectLocalByBridgeInterface{
		ifLocal:    fmt.Sprintf("(%s)", interfaceName),
		ifNotLocal: fmt.Sprintf("! (%s)", interfaceName),
		toAddress:  fmt.Sprintf("(%s)", interfaceName),
	}, nil
}

func (d *detectLocalByBridgeInterface) IsImplemented() bool {
	return true
}

func (d *detectLocalByBridgeInterface) IfLocal() string {
	return d.ifLocal
}

func (d *detectLocalByBridgeInterface) IfNotLocal() string {
	return d.ifNotLocal
}

func (d *detectLocalByBridgeInterface) ToAddress() string {
	return d.toAddress
}
