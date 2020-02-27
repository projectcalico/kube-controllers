// Copyright (c) 2017-2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package node

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RateLimitK8s          = "k8s"
	RateLimitCalicoCreate = "calico-create"
	RateLimitCalicoList   = "calico-list"
	RateLimitCalicoUpdate = "calico-update"
	RateLimitCalicoDelete = "calico-delete"
	nodeLabelAnnotation   = "projectcalico.org/kube-labels"
	hepCreatedLabelKey    = "projectcalico.org/created-by"
	hepCreatedLabelValue  = "calico-kube-controllers"
)

var (
	retrySleepTime = 100 * time.Millisecond
)

// NodeController implements the Controller interface.  It is responsible for monitoring
// kubernetes nodes and responding to delete events by removing them from the Calico datastore.
type NodeController struct {
	ctx               context.Context
	informer          cache.Controller
	indexer           cache.Indexer
	calicoClient      client.Interface
	k8sClientset      *kubernetes.Clientset
	rl                workqueue.RateLimiter
	schedule          chan interface{}
	nodemapper        map[string]string
	nodemapLock       sync.Mutex
	syncer            bapi.Syncer
	config            *config.Config
	syncLabels        bool
	autoHostEndpoints bool
	nodeCache         map[string]*api.Node
	syncStatus        bapi.SyncStatus
}

// NewNodeController Constructor for NodeController
func NewNodeController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg *config.Config) controller.Controller {
	nc := &NodeController{
		ctx:               ctx,
		calicoClient:      calicoClient,
		k8sClientset:      k8sClientset,
		rl:                workqueue.DefaultControllerRateLimiter(),
		nodemapper:        map[string]string{},
		config:            cfg,
		autoHostEndpoints: cfg.AutoHostEndpoints == "enabled",
		nodeCache:         make(map[string]*api.Node),
	}

	// channel used to kick the controller into scheduling a sync. It has length
	// 1 so that we coalesce multiple kicks while a sync is happening down to
	// just one additional sync.
	nc.schedule = make(chan interface{}, 1)

	// Create a Node watcher.
	listWatcher := cache.NewListWatchFromClient(k8sClientset.CoreV1().RESTClient(), "nodes", "", fields.Everything())

	// Setup event handlers
	handlers := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			// Just kick controller to wake up and perform a sync. No need to bother what node it was
			// as we sync everything.
			kick(nc.schedule)
		}}

	// Determine if we should sync node labels.
	nc.syncLabels = cfg.SyncNodeLabels && cfg.DatastoreType != "kubernetes"

	if nc.syncLabels {
		// Add handlers for node add/update events from k8s.
		handlers.AddFunc = func(obj interface{}) {
			nc.syncNodeLabels(obj.(*v1.Node))
		}
		handlers.UpdateFunc = func(_, obj interface{}) {
			nc.syncNodeLabels(obj.(*v1.Node))
		}
	}

	// Informer handles managing the watch and signals us when nodes are deleted.
	// also syncs up labels between k8s/calico node objects
	nc.indexer, nc.informer = cache.NewIndexerInformer(listWatcher, &v1.Node{}, 0, handlers, cache.Indexers{})

	if nc.syncLabels || nc.autoHostEndpoints {
		// Start the syncer.
		nc.initSyncer()
		nc.syncer.Start()
	}

	return nc
}

// getK8sNodeName is a helper method that searches a calicoNode for its kubernetes nodeRef.
func getK8sNodeName(calicoNode api.Node) string {
	for _, orchRef := range calicoNode.Spec.OrchRefs {
		if orchRef.Orchestrator == "k8s" {
			return orchRef.NodeName
		}
	}
	return ""
}

// Run starts the node controller. It does start-of-day preparation
// and then launches worker threads. We ignore reconcilerPeriod and threadiness
// as this controller does not use a cache and runs only one worker thread.
func (c *NodeController) Run(threadiness int, reconcilerPeriod string, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	log.Info("Starting Node controller")

	// Wait till k8s cache is synced
	go c.informer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.informer.HasSynced() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	// Start Calico cache.
	go c.acceptScheduleRequests(stopCh)

	log.Info("Node controller is now running")

	// Kick off a start of day sync. Write non-blocking so that if a sync is
	// already scheduled, we don't schedule another.
	kick(c.schedule)

	<-stopCh
	log.Info("Stopping Node controller")
}

// acceptScheduleRequests monitors the schedule channel for kicks to wake up
// and schedule syncs.
func (c *NodeController) acceptScheduleRequests(stopCh <-chan struct{}) {
	for {
		// Wait until something wakes us up, or we are stopped
		select {
		case <-c.schedule:
			err := c.syncDelete()
			if err != nil {
				// Reschedule the sync since we hit an error. Note that
				// syncDelete() does its own rate limiting, so it's fine to
				// reschedule immediately.
				kick(c.schedule)
			}
		case <-stopCh:
			return
		}
	}
}

func (c *NodeController) syncDelete() error {
	// Call the appropriate cleanup logic based on whether we're using
	// Kubernetes datastore or etecdv3.
	if c.config.DatastoreType == "kubernetes" {
		return c.syncDeleteKDD()
	}
	return c.syncDeleteEtcd()
}

// isAutoHostendpoint determines if the given hostendpoint is managed by
// kube-controllers.
func isAutoHostendpoint(h *api.HostEndpoint) bool {
	v, ok := h.Labels[hepCreatedLabelKey]
	return ok && v == hepCreatedLabelValue
}

// createAutoHostendpoint creates an auto hostendpoint for the specified node.
func (c *NodeController) createAutoHostendpoint(n *api.Node) (*api.HostEndpoint, error) {
	hep := c.generateAutoHostendpointFromNode(n)

	time.Sleep(c.rl.When(RateLimitCalicoCreate))
	res, err := c.calicoClient.HostEndpoints().Create(c.ctx, hep, options.SetOptions{})
	if err != nil {
		log.Warnf("could not create hostendpoint for node: %v", err)
		return nil, err
	}
	c.rl.Forget(RateLimitCalicoCreate)
	return res, nil
}

// kick puts an item on the channel in non-blocking write. This means if there
// is already something pending, it has no effect. This allows us to coalesce
// multiple requests into a single pending request.
func kick(c chan<- interface{}) {
	select {
	case c <- nil:
		// pass
	default:
		// pass
	}
}

// generateAutoHostendpointName returns the auto hostendpoint's name.
func (c *NodeController) generateAutoHostendpointName(nodeName string) string {
	return fmt.Sprintf("%s-auto-hep", nodeName)
}

// getAutoHostendpointExpectedIPs returns all of the known IPs on the node resource
// that should set on the auto hostendpoint.
func (c *NodeController) getAutoHostendpointExpectedIPs(node *api.Node) []string {
	expectedIPs := []string{}
	if node.Spec.BGP != nil {
		// BGP IPv4 and IPv6 addresses are CIDRs.
		if node.Spec.BGP.IPv4Address != "" {
			ip, _, _ := net.ParseCIDROrIP(node.Spec.BGP.IPv4Address)
			expectedIPs = append(expectedIPs, ip.String())
		}
		if node.Spec.BGP.IPv6Address != "" {
			ip, _, _ := net.ParseCIDROrIP(node.Spec.BGP.IPv6Address)
			expectedIPs = append(expectedIPs, ip.String())
		}
		if node.Spec.BGP.IPv4IPIPTunnelAddr != "" {
			expectedIPs = append(expectedIPs, node.Spec.BGP.IPv4IPIPTunnelAddr)
		}
	}
	if node.Spec.IPv4VXLANTunnelAddr != "" {
		expectedIPs = append(expectedIPs, node.Spec.IPv4VXLANTunnelAddr)
	}
	return expectedIPs
}

// generateAutoHostendpointFromNode returns the expected auto hostendpoint to be
// created from the given node.
func (c *NodeController) generateAutoHostendpointFromNode(node *api.Node) *api.HostEndpoint {
	hepLabels := make(map[string]string, len(node.Labels)+1)
	for k, v := range node.Labels {
		hepLabels[k] = v
	}
	hepLabels[hepCreatedLabelKey] = hepCreatedLabelValue

	return &api.HostEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:   c.generateAutoHostendpointName(node.Name),
			Labels: hepLabels,
		},
		Spec: api.HostEndpointSpec{
			Node:          node.Name,
			InterfaceName: "*",
			ExpectedIPs:   c.getAutoHostendpointExpectedIPs(node),
		},
	}
}

// hostendpointNeedsUpdate returns true if the current automatic hostendpoint
// needs to be updated.
func (c *NodeController) hostendpointNeedsUpdate(current *api.HostEndpoint, expected *api.HostEndpoint) bool {
	if !reflect.DeepEqual(current.Labels, expected.Labels) {
		return true
	}
	if !reflect.DeepEqual(current.Spec.ExpectedIPs, expected.Spec.ExpectedIPs) {
		return true
	}
	return current.Spec.InterfaceName != expected.Spec.InterfaceName
}

// updateHostendpoint updates the current hostendpoint so that it matches the
// expected hostendpoint.
func (c *NodeController) updateHostendpoint(current *api.HostEndpoint, expected *api.HostEndpoint) error {
	if c.hostendpointNeedsUpdate(current, expected) {
		log.WithField("hostendpoint", current.Name).Debug("hostendpoint needs update")
		expected.ResourceVersion = current.ResourceVersion
		expected.ObjectMeta.CreationTimestamp = current.ObjectMeta.CreationTimestamp
		expected.ObjectMeta.UID = current.ObjectMeta.UID

		time.Sleep(c.rl.When(RateLimitCalicoUpdate))
		_, err := c.calicoClient.HostEndpoints().Update(context.Background(), expected, options.SetOptions{})
		if err == nil {
			c.rl.Forget(RateLimitCalicoUpdate)
		}
		return err
	}
	return nil
}

// syncAutoHostendpoint syncs the auto hostendpoint for the given node, optionally
// retrying the operation a few times until it succeeds.
func (c *NodeController) syncAutoHostendpoint(node *api.Node, attemptRetries bool) error {
	hepName := c.generateAutoHostendpointName(node.Name)

	// On failure, we retry a certain number of times if attempRetries.
	for n := 1; n <= 5; n++ {
		log.Debugf("syncing hostendpoint %q from node %+v. attempt #%v", hepName, node, n)

		// Try getting the host endpoint.
		expectedHep := c.generateAutoHostendpointFromNode(node)
		currentHep, err := c.calicoClient.HostEndpoints().Get(c.ctx, hepName, options.GetOptions{})
		if err != nil {
			switch err.(type) {
			case errors.ErrorResourceDoesNotExist:
				log.Infof("host endpoint %q doesn't exist, creating it", node.Name)
				_, err := c.createAutoHostendpoint(node)
				if err != nil {
					log.WithError(err).Warnf("failed to create host endpoint %q", node.Name)
					if !attemptRetries {
						return err
					}
					time.Sleep(retrySleepTime)
					continue
				}
			default:
				log.WithError(err).Warnf("failed to get host endpoint %q", node.Name)
				if !attemptRetries {
					return err
				}
				time.Sleep(retrySleepTime)
				continue
			}
		} else if err := c.updateHostendpoint(currentHep, expectedHep); err != nil {
			if !attemptRetries {
				return err
			}
			log.WithError(err).Warnf("failed to update hostendpoint %q", currentHep.Name)
			time.Sleep(retrySleepTime)
			continue
		}

		log.WithField("hostendpoint", node.Name).Info("successfully synced hostendpoint")
		return nil
	}
	return fmt.Errorf("too many retries while syncing hostendpoint %q", hepName)
}

// syncAllAutoHostendpoints ensures that the expected auto hostendpoints exist.
func (c *NodeController) syncAllAutoHostendpoints() error {
	autoHeps, err := c.listAutoHostendpoints()
	if err != nil {
		return err
	}

	// Delete any auto hostendpoints that:
	// - reference a Calico node that doesn't exist
	// - remain after autoHostEndpoints has been disabled
	for _, hep := range autoHeps {
		_, hepNodeExists := c.nodeCache[hep.Spec.Node]

		if !hepNodeExists || !c.autoHostEndpoints {
			err := c.deleteHostendpoint(hep.Name)
			if err != nil {
				return err
			}
		}
	}

	// For every Calico node in our cache, create/update the auto hostendpoint
	// for it.
	if c.autoHostEndpoints {
		for _, node := range c.nodeCache {
			err := c.syncAutoHostendpoint(node, false)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// listAutoHostendpoints returns a map of auto hostendpoints keyed by the
// hostendpoint's name.
func (c *NodeController) listAutoHostendpoints() (map[string]api.HostEndpoint, error) {
	heps, err := c.calicoClient.HostEndpoints().List(c.ctx, options.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list hostendpoints: %v", err.Error())
	}
	m := make(map[string]api.HostEndpoint)
	for _, h := range heps.Items {
		if isAutoHostendpoint(&h) {
			m[h.Name] = h
		}
	}
	return m, nil
}

// deleteHostendpoint removes the specified hostendpoint.
func (c *NodeController) deleteHostendpoint(hepName string) error {
	// On failure, we retry a certain number of times.
	for n := 0; n < 5; n++ {
		time.Sleep(c.rl.When(RateLimitCalicoDelete))
		_, err := c.calicoClient.HostEndpoints().Delete(c.ctx, hepName, options.DeleteOptions{})
		if err != nil {
			switch err.(type) {
			// If the hostendpoint does not exist, we will retry anyways since
			// the Calico node may have been created then deleted immediately
			// afterwards such that the hostendpoint wasn't created yet.
			case errors.ErrorResourceDoesNotExist:
				log.Warnf("could not delete host endpoint %q because it does not exist, retrying", hepName)
				continue
			default:
				log.WithError(err).Warnf("could not delete host endpoint %q", hepName)
				continue
			}
		}
		c.rl.Forget(RateLimitCalicoDelete)

		log.Infof("deleted hostendpoint %q", hepName)
		return nil
	}

	return fmt.Errorf("too many retries when deleting hostendpoint %q", hepName)
}
