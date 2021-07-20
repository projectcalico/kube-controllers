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
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DefaultRouteReflectorMeshName   = "rrs-to-rrs"
	DefaultRouteReflectorClientName = "peer-to-rrs-%d"
)

// RouteReflectorStatus Represents the actual and desired state
type RouteReflectorStatus struct {
	Zones       []string
	ActualRRs   int
	ExpectedRRs int
	Nodes       []*corev1.Node
}

// Topology defines the main functionality of a RR topology implemetation
type Topology interface {
	// Node is RR or not based on topology
	IsRouteReflector(string, map[string]string) bool
	// Generates ClusterID of the RR
	GetClusterID(*corev1.Node) string
	// Generates RR label for specific node
	GetNodeLabel(string) (string, string)
	// Returns a filter function to select affected nodes of the current eecution
	GetNodeFilter(*corev1.Node) func(*corev1.Node) bool
	// Calculates current and expected number of RR
	GetRouteReflectorStatuses(map[*corev1.Node]bool) []RouteReflectorStatus
	// Generates BGP peer configurations based on topology
	GenerateBGPPeers(map[*corev1.Node]bool, []*apiv3.BGPPeer) ([]*apiv3.BGPPeer, []*apiv3.BGPPeer)
}

// Config topology config
type Config struct {
	NodeLabelKey      string
	NodeLabelValue    string
	ZoneLabel         string
	HostnameLabel     string
	ClusterID         string
	Min               int
	Max               int
	Ration            float64
	ReflectorsPerNode int
}

// generateBGPPeerStub initialize a new BGPPeer stub
func generateBGPPeerStub(name string) *apiv3.BGPPeer {
	return &apiv3.BGPPeer{
		TypeMeta: metav1.TypeMeta{
			Kind:       apiv3.KindBGPPeer,
			APIVersion: apiv3.GroupVersionCurrent,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

// findBGPPeer find BGP peer by name
func findBGPPeer(peers []*apiv3.BGPPeer, name string) *apiv3.BGPPeer {
	for _, p := range peers {
		if p.GetName() == name {
			return p
		}
	}

	return nil
}

// countActiveNodes Couns active nodes
func countActiveNodes(nodes map[*corev1.Node]bool) (actualNodes int) {
	for _, isReady := range nodes {
		if isReady {
			actualNodes++
		}
	}

	return
}

// countActiveRouteReflectors counts active RRs
func countActiveRouteReflectors(isRouteReflector func(string, map[string]string) bool, nodes map[*corev1.Node]bool) (actualRRs int) {
	for n, isReady := range nodes {
		if isReady && isRouteReflector(string(n.GetUID()), n.GetLabels()) {
			actualRRs++
		}
	}

	return
}
