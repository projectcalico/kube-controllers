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
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
)

const (
	RouteReflectorClusterIDAnnotation = "projectcalico.org/RouteReflectorClusterID"
)

type KddDataStore struct {
	topology topologies.Topology
}

func (d *KddDataStore) RemoveRRStatus(node *apiv3.Node) error {
	nodeLabelKey, _ := d.topology.GetNodeLabel(string(node.GetUID()))
	delete(node.Labels, nodeLabelKey)
	delete(node.Annotations, RouteReflectorClusterIDAnnotation)

	return nil
}

func (d *KddDataStore) AddRRStatus(node *apiv3.Node) error {
	labelKey, labelValue := d.topology.GetNodeLabel(string(node.GetUID()))
	node.Labels[labelKey] = labelValue

	clusterID := d.topology.GetClusterID(string(node.GetUID()), node.GetCreationTimestamp().UnixNano())
	node.Annotations[RouteReflectorClusterIDAnnotation] = clusterID

	return nil
}

func NewKddDatastore(topology *topologies.Topology) Datastore {
	return &KddDataStore{
		topology: *topology,
	}
}
