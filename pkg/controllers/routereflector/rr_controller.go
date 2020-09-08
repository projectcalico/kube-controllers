// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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

package routereflector

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/datastores"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	calicoApiConfig "github.com/projectcalico/libcalico-go/lib/apiconfig"
)

const (
	defaultClusterID              = "224.0.0.0"
	defaultRouteReflectorMin      = 3
	defaultRouteReflectorMax      = 10
	defaultRouteReflectorRatio    = 0.005
	defaultRouteReflectorRatioMin = 0.001
	defaultRouteReflectorRatioMax = 0.05
	defaultRouteReflectorLabel    = "calico-route-reflector"

	shift                         = 100000
	routeReflectorRatioMinShifted = int(defaultRouteReflectorRatioMin * shift)
	routeReflectorRatioMaxShifted = int(defaultRouteReflectorRatioMax * shift)
)

var notReadyTaints = map[string]bool{
	"node.kubernetes.io/not-ready":                   false,
	"node.kubernetes.io/unreachable":                 false,
	"node.kubernetes.io/out-of-disk":                 false,
	"node.kubernetes.io/memory-pressure":             false,
	"node.kubernetes.io/disk-pressure":               false,
	"node.kubernetes.io/network-unavailable":         false,
	"node.kubernetes.io/unschedulable":               false,
	"node.cloudprovider.kubernetes.io/uninitialized": false,
}

var routeReflectorsUnderOperation = map[types.UID]bool{}

type ctrl struct {
	inSync             bool
	syncer             bapi.Syncer
	calicoNodeClient   calicoNodeClient
	k8sNodeClient      k8sNodeClient
	NodeLabelKey       string
	IncompatibleLabels map[string]*string
	Topology           topologies.Topology
	Datastore          datastores.Datastore
	BGPPeer            bgpPeer
	nodes              map[types.UID]*apiv3.Node
}

type calicoNodeClient interface {
	Update(context.Context, *apiv3.Node, options.SetOptions) (*apiv3.Node, error)
}

type k8sNodeClient interface {
	Get(string, metav1.GetOptions) (*corev1.Node, error)
	List(metav1.ListOptions) (*corev1.NodeList, error)
}

func NewRouteReflectorController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg config.GenericControllerConfig) controller.Controller {
	zoneLabel, incompatibleLabels := parseEnv()

	topologyConfig := topologies.Config{
		NodeLabelKey:   defaultRouteReflectorLabel,
		NodeLabelValue: "",
		ZoneLabel:      zoneLabel,
		ClusterID:      defaultClusterID,
		Min:            defaultRouteReflectorMin,
		Max:            defaultRouteReflectorMax,
		Ration:         defaultRouteReflectorRatio,
	}
	log.Infof("Topology config: %v", topologyConfig)

	topology := topologies.NewMultiTopology(topologyConfig)

	var dsType string
	if dsType = os.Getenv("DATASTORE_TYPE"); dsType == "" {
		dsType = string(calicoApiConfig.EtcdV3)
	}

	var datastore datastores.Datastore
	switch dsType {
	case string(calicoApiConfig.Kubernetes):
		datastore = datastores.NewKddDatastore(&topology)
	case string(calicoApiConfig.EtcdV3):
		datastore = datastores.NewEtcdDatastore(&topology, calicoClient)
	default:
		panic(fmt.Errorf("Unsupported DS %s", dsType))
	}

	rrc := &ctrl{
		k8sNodeClient:      k8sClientset.CoreV1().Nodes(),
		calicoNodeClient:   calicoClient.Nodes(),
		NodeLabelKey:       topologyConfig.NodeLabelKey,
		IncompatibleLabels: incompatibleLabels,
		Topology:           topology,
		Datastore:          datastore,
		BGPPeer:            newBGPPeer(calicoClient),
		nodes:              make(map[types.UID]*apiv3.Node),
	}
	rrc.initSyncer()
	return rrc
}

func (c *ctrl) initSyncer() {
	// TODO: Also watch BGP peers  so we can correct if necessary?
	resourceTypes := []watchersyncer.ResourceType{
		{
			ListInterface: model.ResourceListOptions{Kind: apiv3.KindNode},
		},
	}
	type accessor interface {
		Backend() bapi.Client
	}
	c.syncer = watchersyncer.New(c.calicoNodeClient.(accessor).Backend(), resourceTypes, c)
}

func (c *ctrl) OnStatusUpdated(status bapi.SyncStatus) {
	log.Infof("RR syncer status updated: %s", status)
	if status == bapi.InSync {
		log.Infof("In sync with data store, perform reconciliation")
		c.inSync = true
		for _, n := range c.nodes {
			c.reconcile(n)
		}
	}
}

func (c *ctrl) OnUpdates(updates []bapi.Update) {
	// Update local cache.
	log.Debug("RR syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			log.Debug("New node")
			fallthrough
		case bapi.UpdateTypeKVUpdated:
			log.Debug("Node updated")
			// TODO: For some reason, syncer doesn't give revision on the KVPair.
			// So, we need to set it here.
			n := upd.KVPair.Value.(*apiv3.Node)
			n.ResourceVersion = upd.Revision
			c.nodes[n.GetUID()] = n

			if c.inSync {
				c.reconcile(n)
			}
		case bapi.UpdateTypeKVDeleted:
			log.Debug("Node deleted")
			n := upd.KVPair.Value.(*apiv3.Node)
			c.delete(n)
			delete(c.nodes, n.GetUID())
		default:
			log.Errorf("Unhandled update type")
		}
	}
	log.Debug("Node cache: %#v", c.nodes)

	// Then, reconcile RR state if we're in sync.
	if !c.inSync {
		log.Infof("Not yet in sync - wait for reconciliation")
		return
	}

}

func (c *ctrl) delete(node *apiv3.Node) error {
	if c.Topology.IsRouteReflector(string(node.GetUID()), node.GetLabels()) && node.GetDeletionTimestamp() != nil {
		kubeNode, err := c.k8sNodeClient.Get(node.GetName(), metav1.GetOptions{})
		if err != nil {
			log.Errorf("Unable to fetch kubew node %s because of %s", node.GetName(), err.Error())
			return err
		}

		if !isNodeReady(kubeNode) || !isNodeSchedulable(kubeNode) || !c.isNodeCompatible(kubeNode) {
			// Node is deleted right now or has some issues, better to remove form RRs
			if err := c.removeRRStatus(node); err != nil {
				log.Errorf("Unable to cleanup label on %s because of %s", node.GetName(), err.Error())
				return err
			}

			log.Infof("Label was removed from node %s time to re-reconcile", node.GetName())
		}
	}

	return nil
}

func (c *ctrl) reconcile(node *apiv3.Node) error {
	listOptions := c.Topology.NewNodeListOptions(node.GetLabels())
	log.Debugf("List options are %v", listOptions)
	nodeList, err := c.k8sNodeClient.List(listOptions)
	if err != nil {
		log.Errorf("Unable to list nodes because of %s", err.Error())
		return err
	}
	log.Debugf("Total number of nodes %d", len(nodeList.Items))

	nodes := c.collectNodeInfo(nodeList.Items)

	if err := c.revertFailedModification(nodes); err != nil {
		return err
	} else if err := c.updateNodeLabels(nodes); err != nil {
		return err
	} else if err := c.updateBGPTopology(nodes); err != nil {
		return err
	}

	return nil
}

func (c *ctrl) revertFailedModification(nodes map[*apiv3.Node]bool) error {
	for n := range nodes {
		status, ok := routeReflectorsUnderOperation[n.GetUID()]
		if !ok {
			continue
		}
		// Node was under operation, better to revert it
		var err error
		if status {
			err = c.Datastore.RemoveRRStatus(n)
		} else {
			err = c.Datastore.AddRRStatus(n)
		}
		if err != nil {
			log.Errorf("Failed to revert node %s because of %s", n.GetName(), err.Error())
			return err
		}

		log.Infof("Revert route reflector label on %s to %t", n.GetName(), !status)
		if _, ok := c.nodes[n.GetUID()]; ok {
			if _, err := c.calicoNodeClient.Update(context.Background(), n, options.SetOptions{}); err != nil {
				log.Errorf("Failed to revert update node %s because of %s", n.GetName(), err.Error())
				return err
			}
		}

		delete(routeReflectorsUnderOperation, n.GetUID())

		return nil
	}

	return nil
}

func (c *ctrl) updateNodeLabels(nodes map[*apiv3.Node]bool) error {
	missingRouteReflectors := 0

	for _, status := range c.Topology.GetRouteReflectorStatuses(nodes) {
		status.ExpectedRRs += missingRouteReflectors
		log.Infof("Route refletor status in zone(s) %v (actual/expected) = %d/%d", status.Zones, status.ActualRRs, status.ExpectedRRs)

		for _, n := range status.Nodes {
			isReady := nodes[n]
			if !isReady {
				continue
			} else if status.ExpectedRRs == status.ActualRRs {
				break
			}

			if diff := status.ExpectedRRs - status.ActualRRs; diff != 0 {
				if updated, err := c.updateRRStatus(n, diff); err != nil {
					log.Errorf("Unable to update node %s because of %s", n.GetName(), err.Error())
					return err
				} else if updated && diff > 0 {
					status.ActualRRs++
				} else if updated && diff < 0 {
					status.ActualRRs--
				}
			}
		}

		if status.ExpectedRRs != status.ActualRRs {
			log.Infof("Actual number %d is different than expected %d", status.ActualRRs, status.ExpectedRRs)
			missingRouteReflectors = status.ExpectedRRs - status.ActualRRs
		}
	}

	if missingRouteReflectors != 0 {
		log.Errorf("Actual number is different than expected, missing: %d", missingRouteReflectors)
	}

	return nil
}

func (c *ctrl) updateBGPTopology(nodes map[*apiv3.Node]bool) error {
	rrListOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("!%s", c.NodeLabelKey),
	}
	log.Debugf("RR list options are %v", rrListOptions)

	rrList, err := c.k8sNodeClient.List(rrListOptions)
	if err != nil {
		log.Errorf("Unable to list route reflectors because of %s", err.Error())
		return err
	}
	log.Debugf("Route reflectors are: %v", rrList.Items)

	existingBGPPeers, err := c.BGPPeer.list()
	if err != nil {
		log.Errorf("Unable to list BGP peers because of %s", err.Error())
		return err
	}

	log.Debugf("Existing BGPeers are: %v", existingBGPPeers.Items)

	toRefresh, toRemove := c.Topology.GenerateBGPPeers(rrList.Items, nodes, existingBGPPeers)

	log.Infof("Number of BGPPeers to refresh: %v", len(toRefresh))
	log.Debugf("To refresh BGPeers are: %v", toRefresh)
	log.Infof("Number of BGPPeers to delete: %v", len(toRemove))
	log.Debugf("To delete BGPeers are: %v", toRemove)

	for _, bp := range toRefresh {
		log.Infof("Saving %s BGPPeer", bp.Name)
		if err := c.BGPPeer.save(&bp); err != nil {
			log.Errorf("Unable to save BGPPeer because of %s", err.Error())
			return err
		}
	}

	// Give some time to Calico to establish new connections before deleting old ones
	if len(toRefresh) > 0 {
		time.Sleep(2)
	}

	for _, p := range toRemove {
		log.Debugf("Removing BGPPeer: %s", p.GetName())
		if err := c.BGPPeer.remove(&p); err != nil {
			log.Errorf("Unable to remove BGPPeer because of %s", err.Error())
			return err
		}
	}

	return nil
}

func (c *ctrl) removeRRStatus(node *apiv3.Node) error {
	routeReflectorsUnderOperation[node.GetUID()] = false

	if err := c.Datastore.RemoveRRStatus(node); err != nil {
		log.Errorf("Unable to cleanup RR status %s because of %s", node.GetName(), err.Error())
		return err
	}

	log.Infof("Removing route reflector label from %s", node.GetName())
	node, err := c.calicoNodeClient.Update(context.Background(), node, options.SetOptions{})
	if err != nil {
		log.Errorf("Unable to cleanup node %s because of %s", node.GetName(), err.Error())
		return err
	}

	delete(routeReflectorsUnderOperation, node.GetUID())

	return nil
}

func (c *ctrl) updateRRStatus(node *apiv3.Node, diff int) (bool, error) {
	labeled := c.Topology.IsRouteReflector(string(node.GetUID()), node.GetLabels())
	if labeled && diff > 0 || !labeled && diff < 0 {
		return false, nil
	}

	routeReflectorsUnderOperation[node.GetUID()] = true

	if diff < 0 {
		if err := c.Datastore.RemoveRRStatus(node); err != nil {
			log.Errorf("Unable to delete RR status %s because of %s", node.GetName(), err.Error())
			return false, err
		}
		log.Infof("Removing route reflector label to %s", node.GetName())
	} else {
		if err := c.Datastore.AddRRStatus(node); err != nil {
			log.Errorf("Unable to add RR status %s because of %s", node.GetName(), err.Error())
			return false, err
		}
		log.Infof("Adding route reflector label to %s", node.GetName())
	}

	node, err := c.calicoNodeClient.Update(context.Background(), node, options.SetOptions{})
	if err != nil {
		log.Errorf("Unable to update node %s because of %s", node.GetName(), err.Error())
		return false, err
	}

	delete(routeReflectorsUnderOperation, node.GetUID())

	return true, nil
}

func (c *ctrl) collectNodeInfo(allNodes []corev1.Node) (filtered map[*apiv3.Node]bool) {
	filtered = map[*apiv3.Node]bool{}

	for _, n := range allNodes {
		for _, cn := range c.nodes {
			if cn.GetUID() == n.GetUID() {
				filtered[cn] = isNodeReady(&n) && isNodeSchedulable(&n) && c.isNodeCompatible(&n)
			}
		}
	}

	return
}

func (c *ctrl) isNodeCompatible(node *corev1.Node) bool {
	for k, v := range node.GetLabels() {
		if iv, ok := c.IncompatibleLabels[k]; ok && (iv == nil || *iv == v) {
			return false
		}
	}

	return true
}

func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == "True"
		}
	}

	return false
}

func isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable == true {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if _, ok := notReadyTaints[taint.Key]; ok {
			return false
		}
	}

	return true
}

func (c *ctrl) Run(stopCh chan struct{}) {
	// Start the syncer.
	go c.syncer.Start()

	<-stopCh
	log.Info("Stopping route reflector controller")
}
