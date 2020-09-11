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
	"errors"
	"testing"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEtcdRemoveRRStatusUpdateError(t *testing.T) {
	kubeNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
	}

	calicoNode := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
		Spec: apiv3.NodeSpec{
			BGP: &apiv3.NodeBGPSpec{},
		},
	}

	ds := EtcdDataStore{
		nodeInfo: mockTopology{
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			update: func(*apiv3.Node) (*apiv3.Node, error) {
				return nil, errors.New("fatal error")
			},
		},
	}

	err := ds.RemoveRRStatus(kubeNode, calicoNode)

	if err == nil {
		t.Error("Error not found")
	}
}

func TestEtcdRemoveRRStatus(t *testing.T) {
	kubeNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
	}

	calicoNode := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
		Spec: apiv3.NodeSpec{
			BGP: &apiv3.NodeBGPSpec{},
		},
	}

	var nodeToUpdate *apiv3.Node
	ds := EtcdDataStore{
		nodeInfo: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			update: func(node *apiv3.Node) (*apiv3.Node, error) {
				nodeToUpdate = node
				return node, nil
			},
		},
	}

	err := ds.RemoveRRStatus(kubeNode, calicoNode)

	if err != nil {
		t.Errorf("Error found %s", err.Error())
	}
	if _, ok := kubeNode.GetLabels()["rr"]; ok {
		t.Errorf("Label was no removed %v", kubeNode.GetLabels())
	}
	if nodeToUpdate.Spec.BGP.RouteReflectorClusterID != "" {
		t.Errorf("Wrong RouteReflectorClusterID was configured %s", nodeToUpdate.Spec.BGP.RouteReflectorClusterID)
	}
}

func TestEtcdAddRRStatus(t *testing.T) {
	kubeNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kubernetes.io/hostname": "node",
			},
		},
	}

	calicoNode := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kubernetes.io/hostname": "node",
			},
		},
		Spec: apiv3.NodeSpec{
			BGP: &apiv3.NodeBGPSpec{},
		},
	}

	var nodeToUpdate *apiv3.Node
	ds := EtcdDataStore{
		nodeInfo: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", "value"
			},
		},
		calicoClient: mockCalicoClient{
			update: func(node *apiv3.Node) (*apiv3.Node, error) {
				nodeToUpdate = node
				return node, nil
			},
		},
	}

	err := ds.AddRRStatus(kubeNode, calicoNode)

	if err != nil {
		t.Errorf("Error found %s", err.Error())
	}
	if _, ok := kubeNode.GetLabels()["rr"]; !ok {
		t.Errorf("Label was not added %v", kubeNode.GetLabels())
	}
	if nodeToUpdate.Spec.BGP.RouteReflectorClusterID != "clusterID" {
		t.Errorf("Wrong RouteReflectorClusterID was configured %s", nodeToUpdate.Spec.BGP.RouteReflectorClusterID)
	}
}
