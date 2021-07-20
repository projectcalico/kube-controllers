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

package topologies

import (
	"fmt"
	"sort"
	"testing"
	"time"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
		{"1.%d.%d.%d", "first0", 1029384756, "1.4.137.21"},
		{"1.%d.%d.%d", "second0", 1029384756, "1.129.137.21"},
		{"1.%d.%d.%d", "second0", 1029384757, "1.129.234.195"},

		{"1.%d.%d.%d", "first1", 1234567890, "1.119.52.159"},
		{"1.%d.%d.%d", "second1", 1234567890, "1.217.52.159"},
		{"1.%d.%d.%d", "second1", 1234567891, "1.217.66.230"},

		{"1.2.%d.%d", "first2", 1234567890, "1.2.177.52"},
		{"1.2.%d.%d", "second2", 1234567890, "1.2.188.52"},
		{"1.2.%d.%d", "second2", 1234567891, "1.2.188.66"},

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
		node := corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				UID:               types.UID(n.ID),
				CreationTimestamp: metav1.NewTime(time.Unix(n.seed, 0)),
			},
		}
		clusterID := topology.GetClusterID(&node)
		if clusterID != n.output {
			t.Errorf("clusterID is wrong for %s %s != %s", n.ID, n.output, clusterID)
		}
	}
}

func TestGetClusterIDDeterministic(t *testing.T) {
	clusterID := "1.161.52.159"
	topology := &MultiTopology{
		Config: Config{
			ClusterID: "1.%d.%d.%d",
		},
	}

	ID := types.UID("ID")
	cTime := metav1.NewTime(time.Unix(1234567890, 0))

	for i := 0; i < 1000; i++ {
		node := corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				UID:               ID,
				CreationTimestamp: cTime,
			},
		}
		actualCluserID := topology.GetClusterID(&node)
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
		nodes       map[*corev1.Node]bool
		min         int
		max         int
		ratio       float64
		statuses    int
		actualRRs   []int
		expectedRRs []int
	}{
		{
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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
			nodes: map[*corev1.Node]bool{
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

		readyNode := 0
		for _, isReady := range d.nodes {
			if isReady {
				readyNode++
			}
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

	data := map[string]struct {
		nodes         map[*corev1.Node]bool
		existingPeers []*apiv3.BGPPeer
		toRefresh     []*apiv3.BGPPeer
		toDelete      []*apiv3.BGPPeer
	}{
		"Empty list, only rrs-to-rrs must be generate": {
			map[*corev1.Node]bool{},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{},
		},
		"Empty list but rrs-to-rrs exists": {
			map[*corev1.Node]bool{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{},
		},
		"Empty list but rrs-to-rrs different": {
			map[*corev1.Node]bool{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(x)",
						PeerSelector: "has(x)",
					},
				},
			},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{},
		},
		"Empty list but has existing to delete": {
			map[*corev1.Node]bool{},
			[]*apiv3.BGPPeer{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "existing-dummy",
					},
				},
			},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "existing-dummy",
					},
				},
			},
		},
		"Route reflector exists": {
			map[*corev1.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "uid",
						Labels: map[string]string{
							"kubernetes.io/hostname": "node",
						},
					},
				}: true,
			},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{},
		},
		"One route reflector node": {
			map[*corev1.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "rr",
						Labels: map[string]string{
							"rr": "",
						},
					},
				}: true,
			},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
			},
			[]*apiv3.BGPPeer{},
		},
		"One node one route reflector single zone": {
			map[*corev1.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "rrid",
						Labels: map[string]string{
							"rr":                     "4117807680",
							"kubernetes.io/hostname": "rr",
						},
					},
				}: true,
				{
					ObjectMeta: metav1.ObjectMeta{
						UID: "uid",
						Labels: map[string]string{
							"kubernetes.io/hostname": "node",
						},
					},
				}: true,
			},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4117807680-uid",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='4117807680'",
					},
				},
			},
			[]*apiv3.BGPPeer{},
		},
		"Three node three route reflector multi zone": {
			map[*corev1.Node]bool{
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now),
						UID:               "rrid",
						Labels: map[string]string{
							"zone":                   "a",
							"kubernetes.io/hostname": "rr",
							"rr":                     "4158539682",
						},
					},
				}: true,
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now.Add(time.Second)),
						UID:               "rrid2",
						Labels: map[string]string{
							"zone":                   "b",
							"kubernetes.io/hostname": "rr2",
							"rr":                     "4175317301",
						},
					},
				}: true,
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now.Add(time.Second)),
						UID:               "rrid3",
						Labels: map[string]string{
							"zone":                   "c",
							"kubernetes.io/hostname": "rr3",
							"rr":                     "1556604621",
						},
					},
				}: true,
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
				{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(now.Add(time.Second)),
						UID:               "uid3",
						Labels: map[string]string{
							"zone":                   "c",
							"kubernetes.io/hostname": "node3",
						},
					},
				}: true,
			},
			[]*apiv3.BGPPeer{},
			[]*apiv3.BGPPeer{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rrs-to-rrs",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "has(rr)",
						PeerSelector: "has(rr)",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3531741558-uid",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='3531741558'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3531741558-uid2",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='3531741558'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3531741558-uid3",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node3'",
						PeerSelector: "rr=='3531741558'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3548519177-uid",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='3548519177'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3548519177-uid2",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='3548519177'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-3548519177-uid3",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node3'",
						PeerSelector: "rr=='3548519177'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4117807680-uid",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node'",
						PeerSelector: "rr=='4117807680'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4117807680-uid2",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node2'",
						PeerSelector: "rr=='4117807680'",
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindBGPPeer,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "peer-to-rrs-4117807680-uid3",
					},
					Spec: apiv3.BGPPeerSpec{
						NodeSelector: "kubernetes.io/hostname=='node3'",
						PeerSelector: "rr=='4117807680'",
					},
				},
			},
			[]*apiv3.BGPPeer{},
		},
	}

	for name, d := range data {
		t.Run(name, func(t *testing.T) {
			config := Config{
				NodeLabelKey:      "rr",
				ZoneLabel:         "zone",
				ReflectorsPerNode: 3,
			}
			topology := &MultiTopology{
				Config: config,
				single: SingleTopology{
					Config: config,
				},
			}

			toRefresh, toDelete := topology.GenerateBGPPeers(d.nodes, d.existingPeers)

			mapToName := func(i []*apiv3.BGPPeer) (o []string) {
				for _, s := range i {
					o = append(o, s.GetName())
				}
				sort.Strings(o)
				return
			}

			if fmt.Sprintf("%v", mapToName(toRefresh)) != fmt.Sprintf("%v", mapToName(d.toRefresh)) {
				t.Errorf("To refresh is wrong %v \n!=\n %v", mapToName(d.toRefresh), mapToName(toRefresh))
			}
			if fmt.Sprintf("%v", mapToName(toDelete)) != fmt.Sprintf("%v", mapToName(d.toDelete)) {
				t.Errorf("To delete is wrong %v \n!=\n %v", mapToName(d.toDelete), mapToName(toDelete))
			}
		})
	}
}
