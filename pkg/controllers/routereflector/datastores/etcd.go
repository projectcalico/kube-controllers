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

package datastores

import (
	"context"
	"fmt"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/options"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

type calicoNodeClient interface {
	Update(context.Context, *apiv3.Node, options.SetOptions) (*apiv3.Node, error)
}

type EtcdDataStore struct {
	nodeInfo     nodeInfo
	calicoClient calicoNodeClient
}

func (d *EtcdDataStore) GetType() apiconfig.DatastoreType {
	return apiconfig.EtcdV3
}

func (d *EtcdDataStore) RemoveRRStatus(kubeNode *corev1.Node, calicoNode *apiv3.Node) error {
	nodeLabelKey, _ := d.nodeInfo.GetNodeLabel(string(kubeNode.GetUID()))
	delete(kubeNode.Labels, nodeLabelKey)

	return d.updateRouteReflectorClusterID(kubeNode, calicoNode, "")
}

func (d *EtcdDataStore) AddRRStatus(kubeNode *corev1.Node, calicoNode *apiv3.Node) error {
	labelKey, labelValue := d.nodeInfo.GetNodeLabel(string(kubeNode.GetUID()))
	kubeNode.Labels[labelKey] = labelValue

	return d.updateRouteReflectorClusterID(kubeNode, calicoNode, d.nodeInfo.GetClusterID(string(kubeNode.GetUID()), kubeNode.GetCreationTimestamp().UnixNano()))
}

func (d *EtcdDataStore) updateRouteReflectorClusterID(kubeNode *corev1.Node, calicoNode *apiv3.Node, clusterID string) error {
	if calicoNode == nil {
		err := fmt.Errorf("Unable to find Calico node for %s", kubeNode.GetName())
		log.Error(err.Error())
		return err
	}

	calicoNode.Spec.BGP.RouteReflectorClusterID = clusterID

	log.Infof("Adding route reflector cluster ID in %s to '%s' for %s", calicoNode.GetName(), clusterID, kubeNode.GetName())
	if _, err := d.calicoClient.Update(context.Background(), calicoNode, options.SetOptions{}); err != nil {
		log.Errorf("Unable to update Calico node %s because of %s", kubeNode.GetName(), err.Error())
		return err
	}

	return nil
}

func NewEtcdDatastore(topology nodeInfo, calicoClient calicoNodeClient) Datastore {
	return &EtcdDataStore{
		nodeInfo:     topology,
		calicoClient: calicoClient,
	}
}
