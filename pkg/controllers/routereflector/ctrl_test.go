// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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

// import (
// 	"context"
// 	"errors"
// 	"reflect"
// 	"testing"

// 	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
// 	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
// 	corev1 "k8s.io/api/core/v1"
// 	kerrors "k8s.io/apimachinery/pkg/api/errors"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// 	"k8s.io/apimachinery/pkg/runtime"
// 	crunt "sigs.k8s.io/controller-runtime"
// 	"sigs.k8s.io/controller-runtime/pkg/client"
// )

// func TestReconcileGetFatalError(t *testing.T) {
// 	rrcr := ctrl{
// 		Client: mockClient{
// 			get: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeGetError {
// 		t.Error("Wrong exit response not nodeGetError")
// 	}
// }

// func TestReconcileGetNotFound(t *testing.T) {
// 	rrcr := ctrl{
// 		Client: mockClient{
// 			get: func() error {
// 				return &kerrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found: %s", err.Error())
// 	}
// 	if res != nodeNotFound {
// 		t.Error("Wrong exit response not nodeNotFound")
// 	}
// }

// func TestReconcileDeletedRouteReflector(t *testing.T) {
// 	data := []corev1.Node{
// 		//Node not ready
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				DeletionTimestamp: &metav1.Time{},
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "False",
// 					},
// 				},
// 			},
// 		},
// 		//Node unschedulable
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				DeletionTimestamp: &metav1.Time{},
// 			},
// 			Spec: corev1.NodeSpec{
// 				Unschedulable: false,
// 			},
// 		},
// 		//Node has taint not-ready
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				DeletionTimestamp: &metav1.Time{},
// 			},
// 			Spec: corev1.NodeSpec{
// 				Taints: []corev1.Taint{
// 					{
// 						Key:   "node.kubernetes.io/not-ready",
// 						Value: "",
// 					},
// 				},
// 			},
// 		},
// 		//Node not compatible by label
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				DeletionTimestamp: &metav1.Time{},
// 				Labels: map[string]string{
// 					"incompatiblelabel": "",
// 				},
// 			},
// 		},
// 		//Node not compatible by label and value
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				DeletionTimestamp: &metav1.Time{},
// 				Labels: map[string]string{
// 					"incompatiblevalue": "true",
// 				},
// 			},
// 		},
// 	}

// 	iv := "true"
// 	rrcr := ctrl{
// 		IncompatibleLabels: map[string]*string{
// 			"incompatiblelabel": nil,
// 			"incompatiblevalue": &iv,
// 		},
// 		Client: mockClient{},
// 		Topology: mockTopology{
// 			isRouteReflector: func(string) bool {
// 				return true
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 	}

// 	for x, d := range data {
// 		c := rrcr.Client.(mockClient)
// 		c.getNode = &d
// 		rrcr.Client = c

// 		res, err := rrcr.reconcile(crunt.Request{})

// 		if err != nil {
// 			t.Errorf("Error found at %d: %s", x, err.Error())
// 		}
// 		if res != nodeCleaned {
// 			t.Errorf("Wrong exit response at %d not nodeCleaned", x)
// 		}
// 	}
// }

// func TestReconcileDeletedRouteReflectorUpdateError(t *testing.T) {
// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode: &corev1.Node{
// 				ObjectMeta: metav1.ObjectMeta{
// 					DeletionTimestamp: &metav1.Time{},
// 				},
// 				Status: corev1.NodeStatus{
// 					Conditions: []corev1.NodeCondition{
// 						{
// 							Type:   corev1.NodeReady,
// 							Status: "False",
// 						},
// 					},
// 				},
// 			},
// 			update: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		Topology: mockTopology{
// 			isRouteReflector: func(string) bool {
// 				return true
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeCleanupError {
// 		t.Error("Wrong exit response not nodeCleanupError")
// 	}
// }

// func TestReconcileDeletedRouteReflectorRemoveStatusError(t *testing.T) {
// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode: &corev1.Node{
// 				ObjectMeta: metav1.ObjectMeta{
// 					DeletionTimestamp: &metav1.Time{},
// 				},
// 				Status: corev1.NodeStatus{
// 					Conditions: []corev1.NodeCondition{
// 						{
// 							Type:   corev1.NodeReady,
// 							Status: "False",
// 						},
// 					},
// 				},
// 			},
// 		},
// 		Topology: mockTopology{
// 			isRouteReflector: func(string) bool {
// 				return true
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			removeRRStatus: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeCleanupError {
// 		t.Error("Wrong exit response not nodeCleanupError")
// 	}
// }

// func TestReconcileListFailed(t *testing.T) {
// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode: &corev1.Node{
// 				Status: corev1.NodeStatus{
// 					Conditions: []corev1.NodeCondition{
// 						{
// 							Type:   corev1.NodeReady,
// 							Status: "True",
// 						},
// 					},
// 				},
// 			},
// 			list: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		Topology:  mockTopology{},
// 		Datastore: mockDatastore{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeListError {
// 		t.Error("Wrong exit response not nodeListError")
// 	}
// }

// func TestReconcileRevertNeeded(t *testing.T) {
// 	data := []struct {
// 		operation                    bool
// 		removeRRStatusCalled         bool
// 		expectedremoveRRStatusCalled bool
// 		addRRStatusCalled            bool
// 		expectedaddRRStatusCalled    bool
// 	}{
// 		{
// 			operation:                    true,
// 			expectedremoveRRStatusCalled: true,
// 		},
// 		{
// 			operation:                 false,
// 			expectedaddRRStatusCalled: true,
// 		},
// 	}

// 	for x, d := range data {
// 		routeReflectorsUnderOperation["uid"] = d.operation
// 		defer func() {
// 			delete(routeReflectorsUnderOperation, "uid")
// 		}()

// 		rrcr := ctrl{
// 			Client: mockClient{
// 				getNode: &corev1.Node{
// 					Status: corev1.NodeStatus{
// 						Conditions: []corev1.NodeCondition{
// 							{
// 								Type:   corev1.NodeReady,
// 								Status: "True",
// 							},
// 						},
// 					},
// 				},
// 				listResult: []corev1.Node{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							UID: "uid",
// 						},
// 					},
// 				},
// 			},
// 			Topology: mockTopology{},
// 			Datastore: mockDatastore{
// 				removeRRStatus: func() error {
// 					d.removeRRStatusCalled = true
// 					return nil
// 				},
// 				addRRStatus: func() error {
// 					d.addRRStatusCalled = true
// 					return nil
// 				},
// 			},
// 		}

// 		res, err := rrcr.reconcile(crunt.Request{})

// 		if err != nil {
// 			t.Errorf("Error found at %d %s", x, err.Error())
// 		}
// 		if res != nodeReverted {
// 			t.Errorf("Wrong exit response not nodeReverted at %d", x)
// 		}
// 		if d.expectedremoveRRStatusCalled != d.removeRRStatusCalled {
// 			t.Errorf("RemoveRRStatus was not called at %d", x)
// 		}
// 		if d.expectedaddRRStatusCalled != d.addRRStatusCalled {
// 			t.Errorf("RemoveRRStatus was not called at %d", x)
// 		}
// 		if _, ok := routeReflectorsUnderOperation["uid"]; ok {
// 			t.Errorf("Operation doesn't removed at %d", x)
// 		}
// 	}
// }

// func TestReconcileRevertStatusUpdateFailed(t *testing.T) {
// 	data := []struct {
// 		operation      bool
// 		removeRRStatus func() error
// 		addRRStatus    func() error
// 	}{
// 		{
// 			operation: true,
// 			removeRRStatus: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		{
// 			operation: false,
// 			addRRStatus: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 	}

// 	for x, d := range data {
// 		routeReflectorsUnderOperation["uid"] = d.operation
// 		defer func() {
// 			delete(routeReflectorsUnderOperation, "uid")
// 		}()

// 		rrcr := ctrl{
// 			Client: mockClient{
// 				getNode: &corev1.Node{
// 					Status: corev1.NodeStatus{
// 						Conditions: []corev1.NodeCondition{
// 							{
// 								Type:   corev1.NodeReady,
// 								Status: "True",
// 							},
// 						},
// 					},
// 				},
// 				listResult: []corev1.Node{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							UID: "uid",
// 						},
// 					},
// 				},
// 			},
// 			Topology: mockTopology{},
// 			Datastore: mockDatastore{
// 				removeRRStatus: d.removeRRStatus,
// 				addRRStatus:    d.addRRStatus,
// 			},
// 		}

// 		res, err := rrcr.reconcile(crunt.Request{})

// 		if err == nil {
// 			t.Errorf("Error not found at %d", x)
// 		}
// 		if res != nodeRevertError {
// 			t.Errorf("Wrong exit response not nodeRevertError at %d", x)
// 		}
// 	}
// }

// func TestReconcileRevertUpdateFailed(t *testing.T) {
// 	routeReflectorsUnderOperation["uid"] = true
// 	defer func() {
// 		delete(routeReflectorsUnderOperation, "uid")
// 	}()

// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode: &corev1.Node{
// 				Status: corev1.NodeStatus{
// 					Conditions: []corev1.NodeCondition{
// 						{
// 							Type:   corev1.NodeReady,
// 							Status: "True",
// 						},
// 					},
// 				},
// 			},
// 			listResult: []corev1.Node{
// 				{
// 					ObjectMeta: metav1.ObjectMeta{
// 						UID: "uid",
// 					},
// 				},
// 			},
// 			update: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		Topology:  mockTopology{},
// 		Datastore: mockDatastore{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeRevertUpdateError {
// 		t.Error("Wrong exit response not nodeRevertUpdateError")
// 	}
// }

// func TestReconcileAddLabelFailed(t *testing.T) {
// 	defer func() {
// 		delete(routeReflectorsUnderOperation, "uid")
// 	}()

// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			addRRStatus: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		BGPPeer: mockBGPPeer{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeUpdateError {
// 		t.Error("Wrong exit response not nodeUpdateError")
// 	}
// }

// func TestReconcileRemoveLabelFailed(t *testing.T) {
// 	defer func() {
// 		delete(routeReflectorsUnderOperation, "uid")
// 	}()

// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			isRouteReflector: func(string) bool {
// 				return true
// 			},
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   1,
// 						ExpectedRRs: 0,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			removeRRStatus: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		BGPPeer: mockBGPPeer{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeUpdateError {
// 		t.Error("Wrong exit response not nodeUpdateError")
// 	}
// }

// func TestReconcileLabelUpdateFailed(t *testing.T) {
// 	defer func() {
// 		delete(routeReflectorsUnderOperation, "uid")
// 	}()

// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 			update: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer:   mockBGPPeer{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != nodeUpdateError {
// 		t.Error("Wrong exit response not nodeUpdateError")
// 	}
// }

// func TestReconcileRRListFailed(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	listCalled := 0
// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 			list: func() error {
// 				listCalled++
// 				if listCalled != 1 {
// 					return errors.New("fatal error")
// 				}
// 				return nil
// 			},
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer:   mockBGPPeer{},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != rrListError {
// 		t.Error("Wrong exit response not rrListError")
// 	}
// }

// func TestReconcilePeerListFailed(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return nil, errors.New("fatal error")
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != rrPeerListError {
// 		t.Error("Wrong exit response not rrPeerListError")
// 	}
// }

// func TestReconcileSavePeerFailed(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				toRefresh = append(toRefresh, calicoApi.BGPPeer{})
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 			saveBGPPeer: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != bgpPeerError {
// 		t.Error("Wrong exit response not bgpPeerError")
// 	}
// }

// func TestReconcileDeletePeerFailed(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				toRemove = append(toRemove, calicoApi.BGPPeer{})
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 			removeBGPPeer: func() error {
// 				return errors.New("fatal error")
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err == nil {
// 		t.Error("Error not found")
// 	}
// 	if res != bgpPeerRemoveError {
// 		t.Error("Wrong exit response not bgpPeerRemoveError")
// 	}
// }

// func TestReconcileUpscaleRequeueNeeded(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				toRefresh = append(toRefresh, calicoApi.BGPPeer{})
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found %s", err.Error())
// 	}
// 	if res != bgpPeersUpdated {
// 		t.Error("Wrong exit response not bgpPeersUpdated")
// 	}
// }

// func TestReconcileUpscale(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID:    "rr",
// 				Labels: map[string]string{"rr": "0"},
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	addCalled := 0
// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			isRouteReflector: func(UID string) bool {
// 				return UID == "rr"
// 			},
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   1,
// 						ExpectedRRs: 2,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			addRRStatus: func() error {
// 				addCalled++
// 				return nil
// 			},
// 		},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found %s", err.Error())
// 	}
// 	if res != finished {
// 		t.Error("Wrong exit response not finished")
// 	}
// 	if addCalled != 1 {
// 		t.Errorf("Add was called not exactly once %d", addCalled)
// 	}
// }

// func TestReconcileDownscale(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID:    "rr",
// 				Labels: map[string]string{"rr": "0"},
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	removeCalled := 0
// 	rrcr := ctrl{
// 		NodeLabelKey: "rr",
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			isRouteReflector: func(UID string) bool {
// 				return UID == "rr"
// 			},
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   1,
// 						ExpectedRRs: 0,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			removeRRStatus: func() error {
// 				removeCalled++
// 				return nil
// 			},
// 		},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found %s", err.Error())
// 	}
// 	if res != finished {
// 		t.Error("Wrong exit response not finished")
// 	}
// 	if removeCalled != 1 {
// 		t.Errorf("Remove was called not exactly once %d", removeCalled)
// 	}
// }

// func TestReconcileSkipThem(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "False",
// 					},
// 				},
// 			},
// 		},
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid1",
// 			},
// 			Spec: corev1.NodeSpec{
// 				Unschedulable: true,
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid2",
// 			},
// 			Spec: corev1.NodeSpec{
// 				Taints: []corev1.Taint{
// 					{
// 						Key:   "node.kubernetes.io/not-ready",
// 						Value: "",
// 					},
// 				},
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID:    "uid3",
// 				Labels: map[string]string{"incompatible": "true"},
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		IncompatibleLabels: map[string]*string{
// 			"incompatible": nil,
// 		},
// 		Client: mockClient{
// 			getNode: &corev1.Node{
// 				ObjectMeta: metav1.ObjectMeta{
// 					UID: "uid",
// 				},
// 				Status: corev1.NodeStatus{
// 					Conditions: []corev1.NodeCondition{
// 						{
// 							Type:   corev1.NodeReady,
// 							Status: "True",
// 						},
// 					},
// 				},
// 			},
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   0,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{
// 			addRRStatus: func() error {
// 				return errors.New("if you reach this i didn't skipp all nodes")
// 			},
// 		},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found %s", err.Error())
// 	}
// 	if res != finished {
// 		t.Error("Wrong exit response not finished")
// 	}
// }

// func TestReconcileNothing(t *testing.T) {
// 	nodes := []corev1.Node{
// 		{
// 			ObjectMeta: metav1.ObjectMeta{
// 				UID: "uid",
// 			},
// 			Status: corev1.NodeStatus{
// 				Conditions: []corev1.NodeCondition{
// 					{
// 						Type:   corev1.NodeReady,
// 						Status: "True",
// 					},
// 				},
// 			},
// 		},
// 	}

// 	rrcr := ctrl{
// 		Client: mockClient{
// 			getNode:    &nodes[0],
// 			listResult: nodes,
// 		},
// 		Topology: mockTopology{
// 			getRouteReflectorStatuses: func(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 				return []topologies.RouteReflectorStatus{
// 					{
// 						Zones:       []string{"zone"},
// 						ActualRRs:   1,
// 						ExpectedRRs: 1,
// 						Nodes:       getNodeKeys(nodes),
// 					},
// 				}
// 			},
// 			generateBGPPeers: func() (toRefresh []calicoApi.BGPPeer, toRemove []calicoApi.BGPPeer) {
// 				return
// 			},
// 		},
// 		Datastore: mockDatastore{},
// 		BGPPeer: mockBGPPeer{
// 			listBGPPeers: func() (*calicoApi.BGPPeerList, error) {
// 				return &calicoApi.BGPPeerList{}, nil
// 			},
// 		},
// 	}

// 	res, err := rrcr.reconcile(crunt.Request{})

// 	if err != nil {
// 		t.Errorf("Error found %s", err.Error())
// 	}
// 	if res != finished {
// 		t.Error("Wrong exit response not finished")
// 	}
// }

// type mockClient struct {
// 	get        func() error
// 	getNode    *corev1.Node
// 	update     func() error
// 	list       func() error
// 	listResult []corev1.Node
// }

// func (m mockClient) Get(_ context.Context, _ client.ObjectKey, obj runtime.Object) error {
// 	if m.getNode != nil {
// 		v := reflect.ValueOf(obj).Elem()
// 		v.Set(reflect.ValueOf(*m.getNode))
// 	}
// 	if m.get != nil {
// 		return m.get()
// 	}
// 	return nil
// }

// func (m mockClient) Update(context.Context, runtime.Object, ...client.UpdateOption) error {
// 	if m.update != nil {
// 		return m.update()
// 	}
// 	return nil
// }

// func (m mockClient) List(_ context.Context, obj runtime.Object, _ ...client.ListOption) error {
// 	if m.listResult != nil {
// 		obj.(*corev1.NodeList).Items = m.listResult
// 	}
// 	if m.list != nil {
// 		return m.list()
// 	}
// 	return nil
// }

// type mockTopology struct {
// 	isRouteReflector          func(string) bool
// 	getClusterID              func() string
// 	getNodeLabel              func() (string, string)
// 	newNodeListOptions        func() client.ListOptions
// 	getRouteReflectorStatuses func(map[*corev1.Node]bool) []topologies.RouteReflectorStatus
// 	generateBGPPeers          func() ([]calicoApi.BGPPeer, []calicoApi.BGPPeer)
// }

// func (m mockTopology) IsRouteReflector(UID string, _ map[string]string) bool {
// 	if m.isRouteReflector != nil {
// 		return m.isRouteReflector(UID)
// 	}
// 	return false
// }

// func (m mockTopology) GetClusterID(string, int64) string {
// 	return m.getClusterID()
// }

// func (m mockTopology) GetNodeLabel(string) (string, string) {
// 	return m.getNodeLabel()
// }

// func (m mockTopology) NewNodeListOptions(labels map[string]string) client.ListOptions {
// 	if m.newNodeListOptions != nil {
// 		return m.newNodeListOptions()
// 	}
// 	return client.ListOptions{}
// }

// func (m mockTopology) GetRouteReflectorStatuses(nodes map[*corev1.Node]bool) []topologies.RouteReflectorStatus {
// 	return m.getRouteReflectorStatuses(nodes)
// }

// func (m mockTopology) GenerateBGPPeers([]corev1.Node, map[*corev1.Node]bool, *calicoApi.BGPPeerList) ([]calicoApi.BGPPeer, []calicoApi.BGPPeer) {
// 	return m.generateBGPPeers()
// }

// type mockDatastore struct {
// 	removeRRStatus func() error
// 	addRRStatus    func() error
// }

// func (m mockDatastore) RemoveRRStatus(*corev1.Node) error {
// 	if m.removeRRStatus != nil {
// 		return m.removeRRStatus()
// 	}
// 	return nil
// }

// func (m mockDatastore) AddRRStatus(*corev1.Node) error {
// 	if m.addRRStatus != nil {
// 		return m.addRRStatus()
// 	}
// 	return nil
// }

// type mockBGPPeer struct {
// 	listBGPPeers  func() (*calicoApi.BGPPeerList, error)
// 	saveBGPPeer   func() error
// 	removeBGPPeer func() error
// }

// func (m mockBGPPeer) list() (*calicoApi.BGPPeerList, error) {
// 	if m.listBGPPeers != nil {
// 		return m.listBGPPeers()
// 	}
// 	return nil, nil
// }

// func (m mockBGPPeer) save(*calicoApi.BGPPeer) error {
// 	if m.saveBGPPeer != nil {
// 		return m.saveBGPPeer()
// 	}
// 	return nil
// }

// func (m mockBGPPeer) remove(*calicoApi.BGPPeer) error {
// 	if m.removeBGPPeer != nil {
// 		return m.removeBGPPeer()
// 	}
// 	return nil
// }

// func getNodeKeys(nodes map[*corev1.Node]bool) (nodePs []*corev1.Node) {
// 	for n := range nodes {
// 		nodePs = append(nodePs, n)
// 	}
// 	return
// }
