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

package topologies

import (
	"fmt"
	"testing"
	"time"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsRouteReflector(t *testing.T) {
	rrKey := "rr"
	topology := &MultiTopology{
		Config: Config{
			NodeLabelKey: rrKey,
		},
	}

	data := []struct {
		ID     string
		labels map[string]string
		isRR   bool
	}{
		{"non-RR", map[string]string{}, false},
		{"RR", map[string]string{rrKey: ""}, true},
	}

	for _, n := range data {
		isRR := topology.IsRouteReflector(n.ID, n.labels)
		if isRR != n.isRR {
			t.Errorf("RR status is wrong %t != %t", n.isRR, isRR)
		}
	}
}

func TestGetClusterID(t *testing.T) {
	data := []struct {
		clusterID string
		ID        string
		seed      int64
		output    string
	}{
		{"1.%d.%d.%d", "first0", 1029384756, "1.4.92.165"},
		{"1.%d.%d.%d", "second0", 1029384756, "1.129.92.165"},
		{"1.%d.%d.%d", "second0", 1029384757, "1.129.12.192"},

		{"1.%d.%d.%d", "first1", 1234567890, "1.119.237.73"},
		{"1.%d.%d.%d", "second1", 1234567890, "1.217.237.73"},
		{"1.%d.%d.%d", "second1", 1234567891, "1.217.33.5"},

		{"1.2.%d.%d", "first2", 1234567890, "1.2.177.237"},
		{"1.2.%d.%d", "second2", 1234567890, "1.2.188.237"},
		{"1.2.%d.%d", "second2", 1234567891, "1.2.188.33"},

		{"1.2.3.%d", "first3", 1234567890, "1.2.3.142"},
		{"1.2.3.%d", "second3", 1234567890, "1.2.3.186"},
		{"1.2.3.%d", "second3", 1234567891, "1.2.3.186"},
	}

	for _, n := range data {
		topology := &MultiTopology{
			Config: Config{
				ClusterID: n.clusterID,
			},
		}
		clusterID := topology.GetClusterID(n.ID, n.seed)
		if clusterID != n.output {
			t.Errorf("clusterID is wrong for %s %s != %s", n.ID, n.output, clusterID)
		}
	}
}

func TestGetClusterIDDeterministic(t *testing.T) {
	clusterID := "1.161.237.73"
	topology := &MultiTopology{
		Config: Config{
			ClusterID: "1.%d.%d.%d",
		},
	}

	for i := 0; i < 1000; i++ {
		actualCluserID := topology.GetClusterID("ID", 1234567890)
		if clusterID != actualCluserID {
			t.Errorf("clusterID is wrong for %s != %s", actualCluserID, clusterID)
		}
	}
}

func TestGetNodeLabel(t *testing.T) {
	data := []struct {
		ID      string
		rrKey   string
		rrValue string
		output  string
	}{
		{"1", "key", "", "873244444"},
		{"2", "key", "", "923577301"},
		{"3", "key", "value", "value-906799682"},
	}

	for _, n := range data {
		topology := &MultiTopology{
			Config: Config{
				NodeLabelKey:   n.rrKey,
				NodeLabelValue: n.rrValue,
			},
		}

		key, value := topology.GetNodeLabel(n.ID)

		if key != n.rrKey {
			t.Errorf("Label key is wrong %s != %s", key, n.rrKey)
		}
		if value != n.output {
			t.Errorf("Label value is wrong %s != %s", value, n.output)
		}
	}
}

func TestGetRouteReflectorStatuses(t *testing.T) {
	data := []struct {
		nodes       map[*apiv3.Node]bool
		min         int
		max         int
		ratio       float64
		statuses    int
		actualRRs   []int
		expectedRRs []int
	}{
		{
			nodes: map[*apiv3.Node]bool{
				{}: false,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{0},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{1},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
			},
			min:         2,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{1},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
				{}: true,
				{}: true,
			},
			min:         1,
			max:         2,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{2},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
				{}: true,
				{}: true,
			},
			min:         1,
			max:         5,
			ratio:       0.5,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{2},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
				{}: true,
				{}: true,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{3},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{}: true,
				{}: true,
				{}: false,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{0},
			expectedRRs: []int{2},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"rr": ""},
					},
				}: true,
				{}: true,
				{}: false,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    1,
			actualRRs:   []int{1},
			expectedRRs: []int{2},
		},
		{
			nodes: map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"zone": "a"},
					},
				}: true,
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"zone": "b"},
					},
				}: true,
			},
			min:         1,
			max:         5,
			ratio:       1,
			statuses:    2,
			actualRRs:   []int{0, 0},
			expectedRRs: []int{1, 1},
		},
	}

	for x, d := range data {
		config := Config{
			NodeLabelKey: "rr",
			ZoneLabel:    "zone",
			Min:          d.min,
			Max:          d.max,
			Ration:       d.ratio,
		}
		topology := &MultiTopology{
			Config: config,
			single: SingleTopology{
				Config: config,
			},
		}

		statuses := topology.GetRouteReflectorStatuses(d.nodes)

		if len(statuses) != d.statuses {
			t.Errorf("Number of statuses is wrong %d != %d", len(statuses), d.statuses)
		}

		for i, s := range statuses {
			if s.ActualRRs != d.actualRRs[i] {
				t.Errorf("Number of actual RRs is wrong at %d %d != %d", x, s.ActualRRs, d.actualRRs[i])
			}
			if s.ExpectedRRs != d.expectedRRs[i] {
				t.Errorf("Number of expected RRs is wrong at %d %d != %d", x, s.ActualRRs, d.expectedRRs[i])
			}
		}
	}
}

func TestGenerateBGPPeers(t *testing.T) {
	now := time.Now()

	data := []struct {
		routeReflectors []corev1.Node
		nodes           map[*apiv3.Node]bool
		existingPeers   *calicoApi.BGPPeerList
		toRefresh       []calicoApi.BGPPeer
		toDelete        []calicoApi.BGPPeer
	}{
		// Empty list, only rrs-to-rrs must be generate
		{
			[]corev1.Node{},
			map[*apiv3.Node]bool{},
			&calicoApi.BGPPeerList{},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]calicoApi.BGPPeer{},
		},
		// Empty list but rrs-to-rrs exists
		{
			[]corev1.Node{},
			map[*apiv3.Node]bool{},
			&calicoApi.BGPPeerList{
				Items: []calicoApi.BGPPeer{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       calicoApi.KindBGPPeer,
							APIVersion: calicoApi.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "rrs-to-rrs",
						},
						Spec: calicoApi.BGPPeerSpec{
							NodeSelector: "has(rr)",
							PeerSelector: "has(rr)",
						},
					},
				},
			},
			[]calicoApi.BGPPeer{},
			[]calicoApi.BGPPeer{},
		},
		// Empty list but rrs-to-rrs different
		{
			[]corev1.Node{},
			map[*apiv3.Node]bool{},
			&calicoApi.BGPPeerList{
				Items: []calicoApi.BGPPeer{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       calicoApi.KindBGPPeer,
							APIVersion: calicoApi.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "rrs-to-rrs",
						},
						Spec: calicoApi.BGPPeerSpec{
							NodeSelector: "has(x)",
							PeerSelector: "has(x)",
						},
					},
				},
			},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]calicoApi.BGPPeer{},
		},
		// Empty list but has existing to delete
		{
			[]corev1.Node{
				{},
			},
			map[*apiv3.Node]bool{},
			&calicoApi.BGPPeerList{
				Items: []calicoApi.BGPPeer{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "existing-dummy",
						},
					},
				},
			},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]calicoApi.BGPPeer{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "existing-dummy",
					},
				},
			},
		},
		// Route reflector exists
		{
			[]corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rr",
						UID:  "uid",
					},
				},
			},
			map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "uid",
						Labels: map[string]string{
							"kubernetes.io/hostname": "node",
						},
					},
				}: true,
			},
			&calicoApi.BGPPeerList{
				Items: []calicoApi.BGPPeer{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       calicoApi.KindBGPPeer,
							APIVersion: calicoApi.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "rrs-to-rrs",
						},
						Spec: calicoApi.BGPPeerSpec{
							NodeSelector: "has(rr)",
							PeerSelector: "has(rr)",
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       calicoApi.KindBGPPeer,
							APIVersion: calicoApi.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "peer-to-rrs-1556604621-uid",
						},
						Spec: calicoApi.BGPPeerSpec{
							NodeSelector: "kubernetes.io/hostname=='node'",
							PeerSelector: "rr=='1556604621'",
						},
					},
				},
			},
			[]calicoApi.BGPPeer{},
			[]calicoApi.BGPPeer{},
		},
		// One route reflector node
		{
			[]corev1.Node{},
			map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "rr",
						Labels: map[string]string{
							"rr": "",
						},
					},
				}: true,
			},
			&calicoApi.BGPPeerList{},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]calicoApi.BGPPeer{},
		},
		// One node one route reflector single zone
		{
			[]corev1.Node{
				{},
			},
			map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "uid",
						Labels: map[string]string{
							"kubernetes.io/hostname": "node",
						},
					},
				}: true,
			},
			&calicoApi.BGPPeerList{},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-2166136261-uid",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='2166136261'",
					},
				},
			},
			[]calicoApi.BGPPeer{},
		},
		// Three node three route reflector multi zone
		{
			[]corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rr",
						CreationTimestamp: metav1.NewTime(now),
						UID:               "uid",
						Labels: map[string]string{
							"zone": "a",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rr2",
						CreationTimestamp: metav1.NewTime(now.Add(time.Second)),
						UID:               "rr2",
						Labels: map[string]string{
							"zone": "b",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rr3",
						CreationTimestamp: metav1.NewTime(now.Add(2 * time.Second)),
						UID:               "rr3",
						Labels: map[string]string{
							"zone": "a",
						},
					},
				},
			},
			map[*apiv3.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now),
						UID:               "uid",
						Labels: map[string]string{
							"zone":                   "a",
							"kubernetes.io/hostname": "node",
						},
					},
				}: true,
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now.Add(time.Second)),
						UID:               "uid2",
						Labels: map[string]string{
							"zone":                   "b",
							"kubernetes.io/hostname": "node2",
						},
					},
				}: true,
			},
			&calicoApi.BGPPeerList{},
			[]calicoApi.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4158539682-uid",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='4158539682'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4175317301-uid",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='4175317301'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-1556604621-uid",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='1556604621'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4175317301-uid2",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='4175317301'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-1556604621-uid2",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='1556604621'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicoApi.KindBGPPeer,
						APIVersion: calicoApi.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4158539682-uid2",
					},
					Spec: calicoApi.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='4158539682'",
					},
				},
			},
			[]calicoApi.BGPPeer{},
		},
	}

	for x, d := range data {
		config := Config{
			NodeLabelKey: "rr",
			ZoneLabel:    "zone",
		}
		topology := &MultiTopology{
			Config: config,
			single: SingleTopology{
				Config: config,
			},
		}

		toRefresh, toDelete := topology.GenerateBGPPeers(d.routeReflectors, d.nodes, d.existingPeers)

		if fmt.Sprintf("%v", toRefresh) != fmt.Sprintf("%v", d.toRefresh) {
			t.Errorf("To refresh is wrong at %d %v \n!=\n %v", x, toRefresh, d.toRefresh)
		}
		if fmt.Sprintf("%v", toDelete) != fmt.Sprintf("%v", d.toDelete) {
			t.Errorf("To delete is wrong at %d %v \n!=\n %v", x, toDelete, d.toDelete)
		}
	}
}
