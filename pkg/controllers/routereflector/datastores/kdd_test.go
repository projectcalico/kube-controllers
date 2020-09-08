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
	"testing"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKddRemoveRRStatus(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"rr": "0",
			},
			Annotations: map[string]string{
				RouteReflectorClusterIDAnnotation: "clusterID",
			},
		},
	}

	ds := KddDataStore{
		topology: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", ""
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
	if _, ok := node.GetAnnotations()[RouteReflectorClusterIDAnnotation]; ok {
		t.Errorf("Annotation was no removed %v", node.GetLabels())
	}
}

func TestKddAddRRStatus(t *testing.T) {
	node := &apiv3.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kubernetes.io/hostname": "node",
			},
			Annotations: map[string]string{},
		},
	}

	ds := KddDataStore{
		topology: mockTopology{
			getClusterID: func() string {
				return "clusterID"
			},
			getNodeLabel: func() (string, string) {
				return "rr", ""
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
	if value, ok := node.GetAnnotations()[RouteReflectorClusterIDAnnotation]; !ok || value != "clusterID" {
		t.Errorf("Wrong RouteReflectorClusterID was configured %s", value)
	}
}
