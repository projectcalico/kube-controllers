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

package datastores

import (
	"context"
	"fmt"

	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoClient "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	log "github.com/sirupsen/logrus"
)

type nodeClient interface {
	Nodes() calicoClient.NodeInterface
}

type EtcdDataStore struct {
	topology     topologies.Topology
	calicoClient nodeClient
}

func (d *EtcdDataStore) RemoveRRStatus(node *apiv3.Node) error {
	nodeLabelKey, _ := d.topology.GetNodeLabel(string(node.GetUID()))
	delete(node.Labels, nodeLabelKey)

	return d.updateRouteReflectorClusterID(node, "")
}

func (d *EtcdDataStore) AddRRStatus(node *apiv3.Node) error {
	labelKey, labelValue := d.topology.GetNodeLabel(string(node.GetUID()))
	node.Labels[labelKey] = labelValue

	return d.updateRouteReflectorClusterID(node, d.topology.GetClusterID(string(node.GetUID()), node.GetCreationTimestamp().UnixNano()))
}

func (d *EtcdDataStore) updateRouteReflectorClusterID(node *apiv3.Node, clusterID string) error {
	log.Debugf("Fetching Calico node object of %s", node.GetName())
	calicoNodes, err := d.calicoClient.Nodes().List(context.Background(), options.ListOptions{})
	if err != nil {
		log.Errorf("Unable to fetch Calico nodes %s because of %s", node.GetName(), err.Error())
		return err
	}

	var calicoNode *calicoApi.Node
	for _, cn := range calicoNodes.Items {
		if hostname, ok := cn.GetLabels()["kubernetes.io/hostname"]; ok && hostname == node.GetLabels()["kubernetes.io/hostname"] {
			log.Debugf("Calico node found %s for %s-%s", cn.GetName(), node.GetNamespace(), node.GetName())
			calicoNode = &cn
			break
		}
	}
	if calicoNode == nil {
		err := fmt.Errorf("Unable to find Calico node for %s", node.GetName())
		log.Error(err.Error())
		return err
	}

	calicoNode.Spec.BGP.RouteReflectorClusterID = clusterID

	log.Infof("Adding route reflector cluster ID in %s to '%s' for %s", calicoNode.GetName(), clusterID, node.GetName())
	_, err = d.calicoClient.Nodes().Update(context.Background(), calicoNode, options.SetOptions{})
	if err != nil {
		log.Errorf("Unable to update Calico node %s because of %s", node.GetName(), err.Error())
		return err
	}

	return nil
}

func NewEtcdDatastore(topology *topologies.Topology, calicoClient calicoClient.Interface) Datastore {
	return &EtcdDataStore{
		topology:     *topology,
		calicoClient: calicoClient,
	}
}
