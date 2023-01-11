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

// Package app does all of the work necessary to configure and run a
// Kubernetes app process.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	// Enable pprof HTTP handlers.
	_ "net/http/pprof"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	toolswatch "k8s.io/client-go/tools/watch"
	"k8s.io/kubernetes/pkg/proxy"
	proxyconfigapi "k8s.io/kubernetes/pkg/proxy/apis/config"
	"k8s.io/kubernetes/pkg/proxy/pf"
	proxyutil "k8s.io/kubernetes/pkg/proxy/util"
	"k8s.io/utils/exec"

	"k8s.io/klog/v2"
)

// timeoutForNodePodCIDR is the time to wait for allocators to assign a PodCIDR to the
// node after it is registered.
var timeoutForNodePodCIDR = 5 * time.Minute

// platformApplyDefaults is called after parsing command-line flags and/or reading the
// config file, to apply platform-specific default values to config.
func (o *Options) platformApplyDefaults(config *proxyconfigapi.KubeProxyConfiguration) {
	if config.Mode == "" {
		config.Mode = proxyconfigapi.ProxyModePF
	}
	if config.DetectLocalMode == "" {
		klog.V(4).Info("Defaulting detect-local-mode", "localModeClusterCIDR", string(proxyconfigapi.LocalModeClusterCIDR))
		config.DetectLocalMode = proxyconfigapi.LocalModeClusterCIDR
	}
	klog.V(2).InfoS("DetectLocalMode", "localMode", string(config.DetectLocalMode))
}

// platformSetup is called after setting up the ProxyServer, but before creating the
// Proxier. It should fill in any platform-specific fields and perform other
// platform-specific setup.
func (s *ProxyServer) platformSetup(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	if s.Config.DetectLocalMode == proxyconfigapi.LocalModeNodeCIDR {
		logger.Info("Watching for node, awaiting podCIDR allocation", "hostname", s.NodeName)
		node, err := waitForPodCIDR(ctx, s.Client, s.NodeName)
		if err != nil {
			return err
		}
		s.podCIDRs = node.Spec.PodCIDRs
		logger.Info("NodeInfo", "podCIDRs", node.Spec.PodCIDRs)
	}

	return nil
}

// platformCheckSupported is called immediately before creating the Proxier, to check
// what IP families are supported (and whether the configuration is usable at all).
func (s *ProxyServer) platformCheckSupported(ctx context.Context) (ipv4Supported, ipv6Supported, dualStackSupported bool, err error) {
	ipv4Supported = true
	ipv6Supported = true
	dualStackSupported = true
	return
}

// createProxier creates the proxy.Provider
func (s *ProxyServer) createProxier(ctx context.Context, config *proxyconfigapi.KubeProxyConfiguration, dualStack, initOnly bool) (proxy.Provider, error) {
	logger := klog.FromContext(ctx)
	execer := exec.New()

	localDetectors := getLocalDetectors(logger, s.PrimaryIPFamily, config, s.podCIDRs)

	var proxier proxy.Provider
	var err error
	if dualStack {
		proxier, err = pf.NewDualStackProxier(
			ctx,
			execer,
			config.SyncPeriod.Duration,
			config.MinSyncPeriod.Duration,
			localDetectors,
			s.NodeName,
			s.NodeIPs,
			s.Recorder,
			s.HealthzServer,
			config.NodePortAddresses,
		)
	} else {
		proxier, err = pf.NewProxier(
			ctx,
			s.PrimaryIPFamily,
			execer,
			config.SyncPeriod.Duration,
			config.MinSyncPeriod.Duration,
			localDetectors[s.PrimaryIPFamily],
			s.NodeName,
			s.NodeIPs[s.PrimaryIPFamily],
			s.Recorder,
			s.HealthzServer,
			config.NodePortAddresses,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("unable to create proxier: %v", err)
	}

	return proxier, nil
}

func waitForPodCIDR(ctx context.Context, client clientset.Interface, nodeName string) (*v1.Node, error) {
	// since allocators can assign the podCIDR after the node registers, we do a watch here to wait
	// for podCIDR to be assigned, instead of assuming that the Get() on startup will have it.
	ctx, cancelFunc := context.WithTimeout(context.TODO(), timeoutForNodePodCIDR)
	defer cancelFunc()

	fieldSelector := fields.OneTermEqualSelector("metadata.name", nodeName).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
			options.FieldSelector = fieldSelector
			return client.CoreV1().Nodes().List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (i watch.Interface, e error) {
			options.FieldSelector = fieldSelector
			return client.CoreV1().Nodes().Watch(ctx, options)
		},
	}
	condition := func(event watch.Event) (bool, error) {
		// don't process delete events
		if event.Type != watch.Modified && event.Type != watch.Added {
			return false, nil
		}

		n, ok := event.Object.(*v1.Node)
		if !ok {
			return false, fmt.Errorf("event object not of type Node")
		}
		// don't consider the node if is going to be deleted and keep waiting
		if !n.DeletionTimestamp.IsZero() {
			return false, nil
		}
		return n.Spec.PodCIDR != "" && len(n.Spec.PodCIDRs) > 0, nil
	}

	evt, err := toolswatch.UntilWithSync(ctx, lw, &v1.Node{}, nil, condition)
	if err != nil {
		return nil, fmt.Errorf("timeout waiting for PodCIDR allocation to configure detect-local-mode %v: %v", proxyconfigapi.LocalModeNodeCIDR, err)
	}
	if n, ok := evt.Object.(*v1.Node); ok {
		return n, nil
	}
	return nil, fmt.Errorf("event object not of type node")
}

func getLocalDetectors(logger klog.Logger, primaryIPFamily v1.IPFamily, config *proxyconfigapi.KubeProxyConfiguration, nodePodCIDRs []string) map[v1.IPFamily]pf.LocalTrafficDetector {
	localDetectors := map[v1.IPFamily]pf.LocalTrafficDetector{
		v1.IPv4Protocol: pf.NewNoOpLocalDetector(),
		v1.IPv6Protocol: pf.NewNoOpLocalDetector(),
	}

	switch config.DetectLocalMode {
	case proxyconfigapi.LocalModeClusterCIDR:
		for family, cidrs := range proxyutil.MapCIDRsByIPFamily(config.DetectLocal.ClusterCIDRs) {
			localDetectors[family] = pf.NewDetectLocalByCIDR(cidrs[0].String())
		}
		if !localDetectors[primaryIPFamily].IsImplemented() {
			logger.Info("Detect-local-mode set to ClusterCIDR, but no cluster CIDR specified for primary IP family", "ipFamily", primaryIPFamily, "clusterCIDRs", config.DetectLocal.ClusterCIDRs)
		}

	case proxyconfigapi.LocalModeNodeCIDR:
		for family, cidrs := range proxyutil.MapCIDRsByIPFamily(nodePodCIDRs) {
			localDetectors[family] = pf.NewDetectLocalByCIDR(cidrs[0].String())
		}
		if !localDetectors[primaryIPFamily].IsImplemented() {
			logger.Info("Detect-local-mode set to NodeCIDR, but no PodCIDR defined at node for primary IP family", "ipFamily", primaryIPFamily, "podCIDRs", nodePodCIDRs)
		}

	case proxyconfigapi.LocalModeBridgeInterface:
		localDetector := pf.NewDetectLocalByBridgeInterface(config.DetectLocal.BridgeInterface)
		localDetectors[v1.IPv4Protocol] = localDetector
		localDetectors[v1.IPv6Protocol] = localDetector

	default:
		logger.Info("Defaulting to no-op detect-local")
	}

	return localDetectors
}

// platformCleanup removes stale kube-proxy rules that can be safely removed.
func platformCleanup(ctx context.Context, mode proxyconfigapi.ProxyMode, cleanupAndExit bool) error {
	if cleanupAndExit {
		execer := exec.New()
		encounteredError := pf.CleanupLeftovers(execer)
		if encounteredError {
			return errors.New("encountered an error while tearing down rules")
		}
	}
	return nil
}
