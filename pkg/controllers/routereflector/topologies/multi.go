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
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strings"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"

	log "github.com/sirupsen/logrus"
)

// MultiTopology multi cluster topology with unique ClusterID for each RR
type MultiTopology struct {
	Config
	single SingleTopology
}

// IsRouteReflector if has RR label
func (t *MultiTopology) IsRouteReflector(nodeID string, labels map[string]string) bool {
	_, ok := labels[t.NodeLabelKey]
	return ok
}

// GetClusterID generates unique ClusterID for each node
func (t *MultiTopology) GetClusterID(node *corev1.Node) string {
	// Counts wild cards in ClusterID to replace
	count := strings.Count(t.ClusterID, "%d")
	parts := make([]interface{}, 0)

	// Generate a random number based on hash of node ID
	rand1 := rand.New(rand.NewSource(int64(getRouteReflectorID(string(node.GetUID())))))
	parts = append(parts, rand1.Int31n(254))

	// Generate more random based on node creation time
	rand2 := rand.New(rand.NewSource(node.GetCreationTimestamp().UnixNano()))
	for len(parts) < count {
		parts = append(parts, rand2.Int31n(254))
	}

	return fmt.Sprintf(t.ClusterID, parts...)
}

// GetNodeLabel RR label for node
func (t *MultiTopology) GetNodeLabel(nodeID string) (string, string) {
	return t.NodeLabelKey, t.getNodeLabel(nodeID)
}

// GetNodeFilter collect all nodes
func (t *MultiTopology) GetNodeFilter(*corev1.Node) func(*corev1.Node) bool {
	return func(*corev1.Node) bool {
		return true
	}
}

// GetRouteReflectorStatuses calculates actual and expected based on node number per zone
func (t *MultiTopology) GetRouteReflectorStatuses(affectedNodes map[*corev1.Node]bool) (statuses []RouteReflectorStatus) {
	// Build metadata
	perZone := map[string]map[*corev1.Node]bool{}
	for n := range affectedNodes {
		zone := n.GetLabels()[t.ZoneLabel]
		if _, ok := perZone[zone]; !ok {
			perZone[zone] = map[*corev1.Node]bool{}
		}
		perZone[zone][n] = affectedNodes[n]
	}

	// Calculate total number of RR
	expRRs := t.single.calculateExpectedNumber(countActiveNodes(affectedNodes))
	// Calculate per zone
	expRRsPerZone := int(math.Ceil(float64(expRRs) / float64(len(perZone))))

	for _, zoneNodes := range perZone {
		// Use single cluster topology for each zone
		status := t.single.GetRouteReflectorStatuses(zoneNodes)[0]

		// Fix previous calculation with multi cluster topology numbers
		status.ActualRRs = countActiveRouteReflectors(t.IsRouteReflector, zoneNodes)
		status.ExpectedRRs = expRRsPerZone

		statuses = append(statuses, status)
	}

	sort.Slice(statuses, func(i, j int) bool {
		return len(statuses[i].Nodes) < len(statuses[j].Nodes)
	})

	return
}

// GenerateBGPPeers generates one mesh for the RRs and a fixed amount of client-RR configs per node
func (t *MultiTopology) GenerateBGPPeers(affectedNodes map[*corev1.Node]bool, existingPeers []*apiv3.BGPPeer) (toRefresh []*apiv3.BGPPeer, toDelete []*apiv3.BGPPeer) {
	routeReflectors := t.collectRouteReflectors(affectedNodes)

	// Sorting RRs and Nodes for deterministic RR for Node selection
	sort.Slice(routeReflectors, func(i, j int) bool {
		return routeReflectors[i].GetCreationTimestamp().UnixNano() < routeReflectors[j].GetCreationTimestamp().UnixNano()
	})

	nodeList := []*corev1.Node{}
	for n := range affectedNodes {
		nodeList = append(nodeList, n)
	}
	// Sorting node list for deterministic node order
	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].GetCreationTimestamp().UnixNano() < nodeList[j].GetCreationTimestamp().UnixNano()
	})

	// Find or generate RR mesh config
	rrConfig := findBGPPeer(existingPeers, DefaultRouteReflectorMeshName)
	if rrConfig == nil {
		log.Debugf("Creating new RR full-mesh BGPPeers: %s", DefaultRouteReflectorMeshName)
		rrConfig = generateBGPPeerStub(DefaultRouteReflectorMeshName)
	}

	toKeep := map[string]bool{}
	selector := fmt.Sprintf("has(%s)", t.NodeLabelKey)
	// Update if different than expected
	if rrConfig.Spec.NodeSelector != selector || rrConfig.Spec.PeerSelector != selector {
		rrConfig.Spec = apiv3.BGPPeerSpec{
			NodeSelector: selector,
			PeerSelector: selector,
		}

		toRefresh = append(toRefresh, rrConfig)
	} else {
		toKeep[rrConfig.GetName()] = true
	}

	// Used for round robin RR selection
	rrIndex := -1
	rrIndexPerZone := map[string]int{}
	zones := []string{}
	rrPerZone := map[string][]*corev1.Node{}

	// Create per zone RR lists for MZR selection
	if t.Config.ZoneLabel != "" {
		for i, rr := range routeReflectors {
			rrZone := rr.GetLabels()[t.Config.ZoneLabel]
			log.Debugf("RR:%s's zone: %s", rr.GetName(), rrZone)
			rrPerZone[rrZone] = append(rrPerZone[rrZone], routeReflectors[i])
		}

		for zone := range rrPerZone {
			zones = append(zones, zone)
		}
		sort.Strings(zones)
	}

	// Let's find RRs for each nodes
	for _, n := range nodeList {
		if t.IsRouteReflector(string(n.GetUID()), n.GetLabels()) {
			continue
		}

		routeReflectorsForNode := []*corev1.Node{}

		// Run MZR selection first for nodes if zone label is configured
		if t.Config.ZoneLabel != "" {
			nodeZone := n.GetLabels()[t.Config.ZoneLabel]
			log.Debugf("Node's zone: %s", nodeZone)

			// Select the 1st RR from the same zone if there're any
			if len(rrPerZone[nodeZone]) > 0 {
				rr := selectRRfromZone(rrIndexPerZone, rrPerZone, nodeZone)
				log.Debugf("Adding %s as 1st RR for Node:%s", rr.GetName(), n.GetName())
				routeReflectorsForNode = append(routeReflectorsForNode, rr)
			}

			// Select the 2nd RR from a different zone
			for _, zone := range zones {
				if zone != nodeZone {
					rr := selectRRfromZone(rrIndexPerZone, rrPerZone, zone)
					log.Debugf("Adding %s as 2nd RR for Node:%s", rr.GetName(), n.GetName())
					routeReflectorsForNode = append(routeReflectorsForNode, rr)
					break
				}
			}
		}

		// Calcualte missing RR peers for node
		peers := int(math.Min(float64(len(routeReflectors)), float64(t.Config.ReflectorsPerNode)))

		// Selecting the remaning RRs sequentially with round robin
		for len(routeReflectorsForNode) < peers {
			rrIndex++
			if rrIndex == len(routeReflectors) {
				// Jump back to first
				rrIndex = 0
			}

			rr := routeReflectors[rrIndex]
			if isAlreadySelected(routeReflectorsForNode, rr) {
				continue
			}

			log.Debugf("Adding %s to RRs of %s", rr.GetName(), n.GetName())
			routeReflectorsForNode = append(routeReflectorsForNode, rr)
		}

		// Node selector template
		nodeSelector := fmt.Sprintf("%s=='%s'", t.Config.HostnameLabel, n.GetLabels()[t.Config.HostnameLabel])

		// Generate or update BGP peer config for node
		for _, rr := range routeReflectorsForNode {
			rrID := getRouteReflectorID(string(rr.GetUID()))
			name := fmt.Sprintf(DefaultRouteReflectorClientName+"-%s", rrID, n.GetUID())

			clientConfig := findBGPPeer(existingPeers, name)
			if clientConfig != nil {
				// The rule is already exists, content check not necessary because they are unique
				toKeep[clientConfig.GetName()] = true
				continue
			}

			// Create new one
			log.Debugf("New BGPPeers: %s", name)
			clientConfig = generateBGPPeerStub(name)
			clientConfig.Spec = apiv3.BGPPeerSpec{
				NodeSelector: nodeSelector,
				PeerSelector: fmt.Sprintf("%s=='%d'", t.NodeLabelKey, rrID),
			}

			log.Debugf("Adding %s to the BGPPeers refresh list", clientConfig.GetName())
			toRefresh = append(toRefresh, clientConfig)
		}
	}

	// Detect obsolate peers to delete
	for i := range existingPeers {
		if _, ok := toKeep[existingPeers[i].GetName()]; !ok && findBGPPeer(toRefresh, existingPeers[i].GetName()) == nil {
			log.Debugf("Adding %s to the BGPPeers delete list", existingPeers[i].GetName())
			toDelete = append(toDelete, existingPeers[i])
		}
	}

	return
}

// getNodeLabel generate RR label for node
func (t *MultiTopology) getNodeLabel(nodeID string) string {
	if t.NodeLabelValue == "" {
		return fmt.Sprintf("%d", getRouteReflectorID(nodeID))
	}
	return fmt.Sprintf("%s-%d", t.NodeLabelValue, getRouteReflectorID(nodeID))
}

// collectRouteReflectors collects RR nodes
func (t *MultiTopology) collectRouteReflectors(nodes map[*corev1.Node]bool) (rrs []*corev1.Node) {
	for n, isReady := range nodes {
		if isReady {
			if _, ok := n.GetLabels()[t.NodeLabelKey]; ok {
				rrs = append(rrs, n)
			}
		}
	}

	return
}

// selectRRfromZone uses round robin for RR seletion per zone
func selectRRfromZone(rrIndexPerZone map[string]int, rrPerZone map[string][]*corev1.Node, zone string) *corev1.Node {
	rrIndexPerZone[zone]++
	if rrIndexPerZone[zone] == len(rrPerZone[zone]) {
		rrIndexPerZone[zone] = 0
	}
	return rrPerZone[zone][rrIndexPerZone[zone]]
}

// isAlreadySelected detects if RR already selected
func isAlreadySelected(rrs []*corev1.Node, r *corev1.Node) bool {
	for i := range rrs {
		if rrs[i].GetName() == r.GetName() {
			return true
		}
	}
	return false
}

// getRouteReflectorID generates hash based on node ID
func getRouteReflectorID(nodeID string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(nodeID))
	return h.Sum32()
}

// NewMultiTopology initialise a new multi cluster topology
func NewMultiTopology(config Config) Topology {
	t := &MultiTopology{
		Config: config,
		single: SingleTopology{
			Config: config,
		},
	}

	// Replace zeros with wild cards to generate unique IP for each RR
	t.ClusterID = strings.Replace(t.ClusterID, ".0", ".%d", -1)

	return t
}
