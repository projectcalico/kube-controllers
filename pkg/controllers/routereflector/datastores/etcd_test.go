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
	"errors"
	"testing"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEtcdRemoveRRStatusListError(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"rr": "0"},
		},
	}

	ds := EtcdDataStore{
		topology: mockTopology{
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			list: func() (*calicoApi.NodeList, error) {
				return nil, errors.New("fatal error")
			},
		},
	}

	err := ds.RemoveRRStatus(node)

	if err == nil {
		t.Error("Error not found")
	}
}

func TestEtcdRemoveRRStatusNodeNotFound(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"rr": "0"},
		},
	}

	ds := EtcdDataStore{
		topology: mockTopology{
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			list: func() (*calicoApi.NodeList, error) {
				return &calicoApi.NodeList{
					Items: []calicoApi.Node{},
				}, nil
			},
		},
	}

	err := ds.RemoveRRStatus(node)

	if err == nil {
		t.Error("Error not found")
	}
}

func TestEtcdRemoveRRStatusUpdateError(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
	}

	ds := EtcdDataStore{
		topology: mockTopology{
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			list: func() (*calicoApi.NodeList, error) {
				return &calicoApi.NodeList{
					Items: []calicoApi.Node{
						{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"rr":                     "0",
									"kubernetes.io/hostname": "node",
								},
							},
							Spec: calicoApi.NodeSpec{
								BGP: &calicoApi.NodeBGPSpec{},
							},
						},
					},
				}, nil
			},
			update: func(*calicoApi.Node) (*calicoApi.Node, error) {
				return nil, errors.New("fatal error")
			},
		},
	}

	err := ds.RemoveRRStatus(node)

	if err == nil {
		t.Error("Error not found")
	}
}

func TestEtcdRemoveRRStatus(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr":                     "0",
				"kubernetes.io/hostname": "node",
			},
		},
	}

	var nodeToUpdate *calicoApi.Node
	ds := EtcdDataStore{
		topology: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", ""
			},
		},
		calicoClient: mockCalicoClient{
			list: func() (*calicoApi.NodeList, error) {
				return &calicoApi.NodeList{
					Items: []calicoApi.Node{
						{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"rr":                     "0",
									"kubernetes.io/hostname": "node",
								},
							},
							Spec: calicoApi.NodeSpec{
								BGP: &calicoApi.NodeBGPSpec{},
							},
						},
					},
				}, nil
			},
			update: func(node *calicoApi.Node) (*calicoApi.Node, error) {
				nodeToUpdate = node
				return node, nil
			},
		},
	}

	err := ds.RemoveRRStatus(node)

	if err != nil {
		t.Errorf("Error found %s", err.Error())
	}
	if _, ok := node.GetLabels()["rr"]; ok {
		t.Errorf("Label was no removed %v", node.GetLabels())
	}
	if nodeToUpdate.Spec.BGP.RouteReflectorClusterID != "" {
		t.Errorf("Wrong RouteReflectorClusterID was configured %s", nodeToUpdate.Spec.BGP.RouteReflectorClusterID)
	}
}

func TestEtcdAddRRStatus(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kubernetes.io/hostname": "node",
			},
		},
	}

	var nodeToUpdate *calicoApi.Node
	ds := EtcdDataStore{
		topology: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", "value"
			},
		},
		calicoClient: mockCalicoClient{
			list: func() (*calicoApi.NodeList, error) {
				return &calicoApi.NodeList{
					Items: []calicoApi.Node{
						{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"kubernetes.io/hostname": "node",
								},
							},
							Spec: calicoApi.NodeSpec{
								BGP: &calicoApi.NodeBGPSpec{},
							},
						},
					},
				}, nil
			},
			update: func(node *calicoApi.Node) (*calicoApi.Node, error) {
				nodeToUpdate = node
				return node, nil
			},
		},
	}

	err := ds.AddRRStatus(node)

	if err != nil {
		t.Errorf("Error found %s", err.Error())
	}
	if _, ok := node.GetLabels()["rr"]; !ok {
		t.Errorf("Label was not added %v", node.GetLabels())
	}
	if nodeToUpdate.Spec.BGP.RouteReflectorClusterID != "clusterID" {
		t.Errorf("Wrong RouteReflectorClusterID was configured %s", nodeToUpdate.Spec.BGP.RouteReflectorClusterID)
	}
}
