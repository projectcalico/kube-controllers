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
	"fmt"
	"os"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/datastores"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
)

const (
	// TODO Make it configurable
	defaultClusterID              = "224.0.0.0"
	defaultRouteReflectorMin      = 3
	defaultRouteReflectorMax      = 10
	defaultRouteReflectorRatio    = 0.005
	defaultRouteReflectorRatioMin = 0.001
	defaultRouteReflectorRatioMax = 0.05
	defaultRouteReflectorLabel    = "calico-route-reflector"
	defaultZoneLabel              = "failure-domain.beta.kubernetes.io/zone"

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

type k8sNodeClient interface {
	Get(string, metav1.GetOptions) (*corev1.Node, error)
	List(metav1.ListOptions) (*corev1.NodeList, error)
	Update(*corev1.Node) (*corev1.Node, error)
}

func NewRouteReflectorController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg config.GenericControllerConfig) controller.Controller {
	topologyConfig := topologies.Config{
		NodeLabelKey:   defaultRouteReflectorLabel,
		NodeLabelValue: "",
		ZoneLabel:      defaultZoneLabel,
		ClusterID:      defaultClusterID,
		Min:            defaultRouteReflectorMin,
		Max:            defaultRouteReflectorMax,
		Ration:         defaultRouteReflectorRatio,
	}
	log.Infof("Topology config: %v", topologyConfig)

	topology := topologies.NewMultiTopology(topologyConfig)

	var dsType string
	if dsType = os.Getenv("DATASTORE_TYPE"); dsType == "" {
		dsType = string(apiconfig.EtcdV3)
	}

	var datastore datastores.Datastore
	switch dsType {
	case string(apiconfig.Kubernetes):
		datastore = datastores.NewKddDatastore(topology)
	case string(apiconfig.EtcdV3):
		datastore = datastores.NewEtcdDatastore(topology, calicoClient.Nodes())
	default:
		panic(fmt.Errorf("Unsupported DS %s", dsType))
	}

	ctrl := &ctrl{
		k8sNodeClient:                 k8sClientset.CoreV1().Nodes(),
		nodeLabelKey:                  topologyConfig.NodeLabelKey,
		incompatibleLabels:            map[string]*string{},
		topology:                      topology,
		datastore:                     datastore,
		bgpPeer:                       newBGPPeer(calicoClient),
		kubeNodes:                     make(map[types.UID]*corev1.Node),
		routeReflectorsUnderOperation: make(map[types.UID]bool),
	}
	ctrl.initSyncers(calicoClient, k8sClientset)
	return ctrl
}
