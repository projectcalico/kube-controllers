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
	"strings"
	"sync"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/datastores"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type k8sNodeClient interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.Node, error)
	List(context.Context, metav1.ListOptions) (*corev1.NodeList, error)
	Update(context.Context, *corev1.Node, metav1.UpdateOptions) (*corev1.Node, error)
}

func NewRouteReflectorController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg config.RouteReflectorControllerConfig) controller.Controller {
	hostnameLabel := orDefaultString(cfg.HostnameLabel, defaultHostnameLabel)

	ctrl := &ctrl{
		updateMutex:                   sync.Mutex{},
		syncWaitGroup:                 &sync.WaitGroup{},
		waitForSyncOnce:               sync.Once{},
		calicoNodeClient:              calicoClient.Nodes(),
		k8sNodeClient:                 k8sClientset.CoreV1().Nodes(),
		bgpPeer:                       newBGPPeer(calicoClient),
		kubeNodes:                     make(map[types.UID]*corev1.Node),
		calicoNodes:                   make(map[string]*apiv3.Node),
		bgpPeers:                      make(map[string]*apiv3.BGPPeer),
		routeReflectorsUnderOperation: make(map[types.UID]bool),
		hostnameLabel:                 hostnameLabel,
	}

	topologyConfig := topologies.Config{
		NodeLabelKey:      orDefaultString(cfg.RouteReflectorLabelKey, defaultRouteReflectorLabelKey),
		NodeLabelValue:    orDefaultString(cfg.RouteReflectorLabelValue, defaultRouteReflectorLabelValue),
		ZoneLabel:         orDefaultString(cfg.ZoneLabel, defaultZoneLabel),
		HostnameLabel:     hostnameLabel,
		ClusterID:         orDefaultString(cfg.ClusterID, defaultClusterID),
		Min:               orDefaultInt(cfg.Min, defaultRouteReflectorMin),
		Max:               orDefaultInt(cfg.Max, defaultRouteReflectorMax),
		Ration:            float64(orDefaultFloat(cfg.Ratio, defaultRouteReflectorRatio)),
		ReflectorsPerNode: orDefaultInt(cfg.RouteReflectorsPerNode, defaultReflectorsPerNode),
	}
	log.Infof("Topology config: %v", topologyConfig)

	switch orDefaultString(cfg.TopologyType, defaultTopologyType) {
	case "multi":
		ctrl.topology = topologies.NewMultiTopology(topologyConfig)
	case "single":
		ctrl.topology = topologies.NewSingleTopology(topologyConfig)
	default:
		panic(fmt.Errorf("Unsupported topology type %s", string(*cfg.TopologyType)))
	}

	switch cfg.DatastoreType {
	case apiconfig.Kubernetes:
		ctrl.datastoreType = apiconfig.Kubernetes
		ctrl.datastore = datastores.NewKddDatastore(ctrl.topology)
	case apiconfig.EtcdV3:
		ctrl.datastoreType = apiconfig.EtcdV3
		ctrl.datastore = datastores.NewEtcdDatastore(ctrl.topology, ctrl.calicoNodeClient)
	default:
		panic(fmt.Errorf("Unsupported Data Store %s", string(cfg.DatastoreType)))
	}

	ctrl.incompatibleLabels = map[string]*string{}
	if cfg.IncompatibleLabels != nil {
		for _, l := range strings.Split(*cfg.IncompatibleLabels, ",") {
			key, value := getKeyValue(strings.Trim(l, " "))
			if strings.Contains(l, "=") {
				ctrl.incompatibleLabels[key] = &value
			} else {
				ctrl.incompatibleLabels[key] = nil
			}
		}
	}

	ctrl.initSyncers(cfg.DatastoreType, calicoClient, k8sClientset)

	return ctrl
}
