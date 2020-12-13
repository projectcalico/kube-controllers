// Copyright (c) 2020 IBM Corporation All rights reserved.
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
	"sync"
	"time"

	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/datastores"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	defaultClusterID                = "224.0.0.0"
	defaultRouteReflectorMin        = 3
	defaultRouteReflectorMax        = 10
	defaultRouteReflectorRatio      = 0.005
	defaultRouteReflectorRatioMin   = 0.001
	defaultRouteReflectorRatioMax   = 0.05
	defaultRouteReflectorLabelKey   = "calico-route-reflector"
	defaultRouteReflectorLabelValue = ""
	defaultZoneLabel                = "failure-domain.beta.kubernetes.io/zone"
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

type ctrl struct {
	updateMutex sync.Mutex

	calicoNodeClient client.NodeInterface
	k8sNodeClient    k8sNodeClient

	kubeNodeInformer cache.Controller
	kubeNodes        map[types.UID]*corev1.Node

	calicoNodeSyncer bapi.Syncer
	calicoNodes      map[string]*apiv3.Node

	bgpPeerSyncer bapi.Syncer
	bgpPeers      map[string]*apiv3.BGPPeer
	bgpPeer       bgpPeer

	routeReflectorsUnderOperation map[types.UID]bool

	topology           topologies.Topology
	datastore          datastores.Datastore
	incompatibleLabels map[string]*string
}

func (c *ctrl) Run(stopCh chan struct{}) {
	if c.datastore.GetType() == apiconfig.EtcdV3 {
		// Start the Calico node syncer.
		go c.calicoNodeSyncer.Start()
	}

	// Start the BGP peersyncer.
	go c.bgpPeerSyncer.Start()

	// Wait till k8s cache is synced
	go c.kubeNodeInformer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.kubeNodeInformer.HasSynced() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	log.Info("Node controller is now running")

	<-stopCh
	log.Info("Stopping route reflector controller")
}

func (c *ctrl) initSyncers(client client.Interface, k8sClientset *kubernetes.Clientset) {
	if kubeNodes, err := k8sClientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{}); err == nil {
		for _, n := range kubeNodes.Items {
			kubeNode := n
			c.kubeNodes[n.GetUID()] = &kubeNode
		}
	}

	type accessor interface {
		Backend() bapi.Client
	}

	calicoNodeResources := []watchersyncer.ResourceType{
		{
			ListInterface: model.ResourceListOptions{Kind: apiv3.KindNode},
		},
	}
	c.calicoNodeSyncer = watchersyncer.New(client.(accessor).Backend(), calicoNodeResources, &calicoNodeSyncer{c})

	bgpPeerResources := []watchersyncer.ResourceType{
		{
			ListInterface: model.ResourceListOptions{Kind: apiv3.KindBGPPeer},
		},
	}
	c.bgpPeerSyncer = watchersyncer.New(client.(accessor).Backend(), bgpPeerResources, &bgpPeerSyncer{c})

	// Create a Node watcher.
	kubeNodeWatcher := cache.NewListWatchFromClient(k8sClientset.CoreV1().RESTClient(), "nodes", "", fields.Everything())

	// Setup event handlers
	handlers := cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.OnKubeUpdate,
		DeleteFunc: c.OnKubeDelete,
	}

	// Informer handles managing the watch and signals us when nodes are deleted.
	// also syncs up labels between k8s/calico node objects
	_, c.kubeNodeInformer = cache.NewIndexerInformer(kubeNodeWatcher, &v1.Node{}, 0, handlers, cache.Indexers{})
}
