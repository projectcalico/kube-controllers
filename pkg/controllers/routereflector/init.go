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

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
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
	ctrl := &ctrl{
		updateMutex:                   sync.Mutex{},
		dsType:                        cfg.DatastoreType,
		calicoNodeClient:              calicoClient.Nodes(),
		k8sNodeClient:                 k8sClientset.CoreV1().Nodes(),
		bgpPeer:                       newBGPPeer(calicoClient),
		kubeNodes:                     make(map[types.UID]*corev1.Node),
		calicoNodes:                   make(map[string]*apiv3.Node),
		bgpPeers:                      make(map[string]*apiv3.BGPPeer),
		routeReflectorsUnderOperation: make(map[types.UID]bool),
	}
	ctrl.updateConfiguration()
	ctrl.initSyncers(calicoClient, k8sClientset)

	return ctrl
}
